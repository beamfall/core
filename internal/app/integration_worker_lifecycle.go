package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/beamfall/core/internal/integrationhost"
)

// Run supervises each installed worker with bounded restart until ctx ends or
// the plugin is deactivated. Deactivation cancels the in-flight worker with a
// typed cause and persists that cancellation in the worker status.
func (m *integrationManager) Run(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if len(m.workers) != 0 {
		return m.runAll(ctx)
	}
	if !m.claimRun() {
		return errIntegrationManagerRunning
	}
	defer m.releaseRun()
	runnable, err := m.workerRunnable(ctx)
	if err != nil {
		return err
	}
	if !runnable {
		return m.reportDeactivation(ctx)
	}

	workerCtx, cancelWorker := context.WithCancelCause(ctx)
	defer cancelWorker(nil)
	done := make(chan error, 1)
	go func() {
		done <- superviseIntegrationWorker(workerCtx, m.runGeneration, m.waitBeforeRestart)
	}()

	ticker := time.NewTicker(pluginActivationPollInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			cancelWorker(context.Cause(ctx))
			retireErr := m.retireGeneration(context.Cause(ctx))
			return errors.Join(retireErr, m.waitForRetirement(done))
		case <-ticker.C:
			runnable, err := m.workerRunnable(ctx)
			if err != nil {
				cancelWorker(err)
				retireErr := m.retireGeneration(err)
				return errors.Join(err, retireErr, m.waitForRetirement(done))
			}
			if runnable {
				continue
			}
			cancelWorker(integrationhost.ErrPluginDeactivated)
			retireErr := m.retireGeneration(integrationhost.ErrPluginDeactivated)
			stopErr := m.waitForRetirement(done)
			return errors.Join(m.reportDeactivation(ctx), retireErr, stopErr)
		}
	}
}

func (m *integrationManager) claimRun() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return false
	}
	m.running = true
	return true
}

func (m *integrationManager) releaseRun() {
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}

func (m *integrationManager) workerRunnable(ctx context.Context) (bool, error) {
	active, err := m.st.PluginActivation(ctx, m.pluginID)
	if err != nil {
		return false, fmt.Errorf("integrationhost: read plugin activation: %w", err)
	}
	if !active {
		return false, nil
	}
	worker, ok, err := m.st.IntegrationWorker(ctx, m.pluginID)
	if err != nil {
		return false, fmt.Errorf("integrationhost: read worker state: %w", err)
	}
	if !ok {
		return false, nil
	}
	return worker.State != "disabled" && worker.State != "failed", nil
}

func (m *integrationManager) runGeneration(ctx context.Context) (result error) {
	lease := m.fence.Acquire()
	runCtx, cancel := context.WithCancelCause(ctx)
	host := integrationhost.NewHost(m.pluginID, m.deps, integrationGrantStore{
		st: m.st, declaredPermissions: m.permissions, manifestBinding: m.manifestBinding,
	}, time.Now)
	sup := integrationhost.NewSupervisor(m.pluginID, m.artifact, host, m.logger, time.Now)
	sup.SetHardeningShim(m.shimPath)
	sup.SetGenerationLease(lease)

	m.mu.Lock()
	m.lease = lease
	m.cancelRun = cancel
	m.supervisor = sup
	m.mu.Unlock()
	defer func() {
		cancel(nil)
		retireErr := m.retireLease(lease)
		m.mu.Lock()
		if m.lease == lease {
			m.lease = nil
			m.cancelRun = nil
			m.supervisor = nil
		}
		m.mu.Unlock()
		result = errors.Join(result, retireErr)
	}()

	run := m.runSupervisor
	if run == nil {
		run = sup.Run
	}
	return run(runCtx)
}

func (m *integrationManager) retireGeneration(cause error) error {
	m.mu.Lock()
	cancel := m.cancelRun
	lease := m.lease
	m.mu.Unlock()
	if cancel != nil {
		cancel(cause)
	}
	return m.retireLease(lease)
}

func (m *integrationManager) retireLease(lease *integrationhost.GenerationLease) error {
	retireCtx, stopRetire := context.WithTimeout(context.Background(), m.configuredStopBound())
	defer stopRetire()
	err := lease.Retire(retireCtx)
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(errIntegrationWorkerStop, err)
	}
	return err
}

func (m *integrationManager) waitForRetirement(done <-chan error) error {
	timer := time.NewTimer(m.configuredStopBound())
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errIntegrationWorkerStop
	}
}

func (m *integrationManager) configuredStopBound() time.Duration {
	if m.stopBound > 0 {
		return m.stopBound
	}
	return integrationWorkerStopBound
}

func (m *integrationManager) waitBeforeRestart(ctx context.Context, delay time.Duration) error {
	if m.waitRestart != nil {
		return m.waitRestart(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

func (m *integrationManager) runAll(ctx context.Context) error {
	type workerResult struct {
		pluginID string
		err      error
	}
	results := make(chan workerResult, len(m.workers))
	for pluginID, worker := range m.workers {
		go func() {
			results <- workerResult{pluginID: pluginID, err: worker.Run(ctx)}
		}()
	}
	remaining := len(m.workers)
	var terminal []error
	for remaining > 0 {
		result := <-results
		remaining--
		if result.err != nil && !errors.Is(result.err, context.Canceled) {
			terminal = append(terminal, fmt.Errorf("%s: %w", result.pluginID, result.err))
			if m.logger != nil && !errors.Is(result.err, integrationhost.ErrPluginDeactivated) {
				m.logger.Error("integration worker supervision stopped", "plugin", result.pluginID, "error", result.err)
			}
		}
	}
	return errors.Join(terminal...)
}

func superviseIntegrationWorker(ctx context.Context, run func(context.Context) error, waitRestart func(context.Context, time.Duration) error) error {
	backoff := time.Second
	for {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		err := run(ctx)
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if errors.Is(err, errIntegrationWorkerStop) {
			return err
		}
		if !errors.Is(err, integrationhost.ErrWorkerExited) {
			return err
		}
		if err := waitRestart(ctx, backoff); err != nil {
			return err
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (m *integrationManager) reportDeactivation(ctx context.Context) error {
	state, ok, err := m.st.IntegrationWorker(ctx, m.pluginID)
	if err != nil {
		return errors.Join(integrationhost.ErrPluginDeactivated, err)
	}
	if ok {
		if state.State != "disabled" {
			state.State = "stopped"
		}
		state.LastError = integrationhost.ErrPluginDeactivated.Error()
		if err := m.st.SetIntegrationWorker(ctx, state, time.Now().UTC()); err != nil {
			return errors.Join(integrationhost.ErrPluginDeactivated, err)
		}
	}
	if m.logger != nil {
		m.logger.Info("integration worker canceled after plugin deactivation", "plugin", m.pluginID)
	}
	return integrationhost.ErrPluginDeactivated
}
