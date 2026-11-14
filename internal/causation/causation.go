// Package causation bounds Integration Worker mutation chains independently of
// any one worker process. It is a stdlib-only leaf owned by the Core process.
package causation

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrInvalidToken rejects an empty, forged, or otherwise malformed
	// server-issued causation token.
	ErrInvalidToken = errors.New("causation: invalid token")
	// ErrChainExpired rejects a chain after its absolute lifetime. Expiration
	// prevents a deliberately slow loop from refilling forever.
	ErrChainExpired = errors.New("causation: chain expired")
	// ErrGlobalBudget rejects a chain after its server-global action budget has
	// been spent, regardless of which workers spent it.
	ErrGlobalBudget = errors.New("causation: global action budget exhausted")
	// ErrWorkerRateLimit rejects a worker after its action window is full.
	ErrWorkerRateLimit = errors.New("causation: worker action rate exceeded")
	// ErrTrackingCapacity fails new chain or worker state closed when its
	// bounded in-memory ledger is full.
	ErrTrackingCapacity = errors.New("causation: tracking capacity exhausted")
	// ErrDescendantCollision rejects an admission whose descendant key is
	// already bound. Nothing is spent or recorded: the caller restamps its rows
	// and asks again, so one key never names rows of two mutations.
	ErrDescendantCollision = errors.New("causation: descendant key already bound")
)

// Token is an opaque, authenticated chain identity issued by Core. Workers may
// echo it but cannot mint a valid replacement to reset a chain's budget.
type Token string

type tokenContextKey struct{}

// WithToken attaches a server-issued causation token to a Host mutation call.
func WithToken(ctx context.Context, token Token) context.Context {
	return context.WithValue(ctx, tokenContextKey{}, token)
}

// TokenFromContext returns the causation token attached at the Host transport
// boundary. A missing token is returned as the zero value and fails closed in
// CausationCap.Admit.
func TokenFromContext(ctx context.Context) Token {
	token, _ := ctx.Value(tokenContextKey{}).(Token)
	return token
}

// Limits configures the independent containment ceilings. Zero or negative
// fields take the corresponding value from DefaultLimits.
type Limits struct {
	GlobalActions      int
	ChainLifetime      time.Duration
	WorkerActions      int
	WorkerWindow       time.Duration
	MaxTrackedChains   int
	MaxTrackedWorkers  int
	DeadLetterRecords  int
	DeadLetterWindow   time.Duration
	DeadLetterCapacity int
	// MaxTrackedDescendants bounds the descendant ledger that binds the
	// outbox rows a mutation appends to that mutation's chain.
	MaxTrackedDescendants int
	// MaxDescendantsPerWorker bounds one worker's share of that ledger, so a
	// worker can exhaust only its own share.
	MaxDescendantsPerWorker int
}

// DefaultLimits returns the ADR-0125 containment contract.
func DefaultLimits() Limits {
	return Limits{
		GlobalActions:      32,
		ChainLifetime:      15 * time.Minute,
		WorkerActions:      60,
		WorkerWindow:       time.Minute,
		MaxTrackedChains:   4096,
		MaxTrackedWorkers:  4096,
		DeadLetterRecords:  8,
		DeadLetterWindow:   time.Minute,
		DeadLetterCapacity: 256,

		MaxTrackedDescendants:   65536,
		MaxDescendantsPerWorker: 16384,
	}
}

func normalizeLimits(limits Limits) Limits {
	defaults := DefaultLimits()
	if limits.GlobalActions <= 0 {
		limits.GlobalActions = defaults.GlobalActions
	}
	if limits.ChainLifetime <= 0 {
		limits.ChainLifetime = defaults.ChainLifetime
	}
	if limits.WorkerActions <= 0 {
		limits.WorkerActions = defaults.WorkerActions
	}
	if limits.WorkerWindow <= 0 {
		limits.WorkerWindow = defaults.WorkerWindow
	}
	if limits.MaxTrackedChains <= 0 {
		limits.MaxTrackedChains = defaults.MaxTrackedChains
	}
	if limits.MaxTrackedWorkers <= 0 {
		limits.MaxTrackedWorkers = defaults.MaxTrackedWorkers
	}
	if limits.DeadLetterRecords <= 0 {
		limits.DeadLetterRecords = defaults.DeadLetterRecords
	}
	if limits.DeadLetterWindow <= 0 {
		limits.DeadLetterWindow = defaults.DeadLetterWindow
	}
	if limits.DeadLetterCapacity <= 0 {
		limits.DeadLetterCapacity = defaults.DeadLetterCapacity
	}
	if limits.MaxTrackedDescendants <= 0 {
		limits.MaxTrackedDescendants = defaults.MaxTrackedDescendants
	}
	if limits.MaxDescendantsPerWorker <= 0 {
		limits.MaxDescendantsPerWorker = defaults.MaxDescendantsPerWorker
	}
	return limits
}

// DeadLetterReason is the closed reason vocabulary exposed by the observable
// containment surface.
type DeadLetterReason string

const (
	DeadLetterInvalidToken     DeadLetterReason = "invalid-token"
	DeadLetterExpiredChain     DeadLetterReason = "expired-chain"
	DeadLetterGlobalBudget     DeadLetterReason = "global-budget"
	DeadLetterWorkerRate       DeadLetterReason = "worker-rate"
	DeadLetterTrackingCapacity DeadLetterReason = "tracking-capacity"
)

// DeadLetter is one coalesced mutation abort. It intentionally carries no
// request payload, URL, file path, or other potentially secret worker data.
type DeadLetter struct {
	WorkerID    string
	CausationID string
	Action      string
	Reason      DeadLetterReason
	FirstAt     time.Time
	LastAt      time.Time
	Occurrences uint64
}

// DeadLetterSnapshot is the bounded operator-observable containment surface.
// Suppressed counts distinct records refused by the per-window record rate;
// Evicted counts oldest records displaced by the fixed retention capacity.
type DeadLetterSnapshot struct {
	Records    []DeadLetter
	Suppressed uint64
	Evicted    uint64
}

type chainState struct {
	actions   int
	expiresAt time.Time
}

type descendant struct {
	token     Token
	workerID  string
	expiresAt time.Time
}

type workerWindow struct {
	startedAt time.Time
	actions   int
}

// CausationCap is the server-global mutation-containment marker and state
// machine. One instance is shared by every worker Host and is retained when an
// individual worker process restarts.
type CausationCap struct {
	limits Limits
	now    func() time.Time

	key        [sha256.Size]byte
	entropyErr error

	mu                sync.Mutex
	chains            map[[sha256.Size]byte]chainState
	workers           map[string]workerWindow
	descendants       map[string]descendant
	workerDescendants map[string]int
	deadLetters       []DeadLetter
	deadWindowStart   time.Time
	deadWindowRecords int
	suppressed        uint64
	evicted           uint64
}

// NewCausationCap constructs an independent cap. Failure to obtain token
// entropy is retained and makes TokenFor fail closed.
func NewCausationCap(limits Limits, now func() time.Time) *CausationCap {
	if now == nil {
		now = time.Now
	}
	cap := &CausationCap{
		limits:  normalizeLimits(limits),
		now:     now,
		chains:  make(map[[sha256.Size]byte]chainState),
		workers: make(map[string]workerWindow),

		descendants:       make(map[string]descendant),
		workerDescendants: make(map[string]int),
	}
	_, cap.entropyErr = rand.Read(cap.key[:])
	return cap
}

// TokenFor issues a deterministic token for one root event within this Core
// process. startedAt is the root event time, making chain lifetime absolute
// rather than refillable by a worker restart or delayed replay.
func (c *CausationCap) TokenFor(root string, startedAt time.Time) (Token, error) {
	if c.entropyErr != nil {
		return "", fmt.Errorf("causation: initialize token key: %w", c.entropyErr)
	}
	if root == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidToken)
	}
	if startedAt.IsZero() {
		startedAt = c.now().UTC()
	}
	rootDigest := sha256.Sum256([]byte(root))
	payload := strconv.FormatInt(startedAt.UTC().UnixMilli(), 10) + "." + base64.RawURLEncoding.EncodeToString(rootDigest[:])
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = mac.Write([]byte(payload))
	return Token(payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))), nil
}

// Admit atomically spends one action from both the chain-global budget and the
// calling worker's rate window. Every denial is sent to the bounded/coalesced
// dead-letter surface before the mutation seam can run.
func (c *CausationCap) Admit(workerID string, token Token, action string) error {
	return c.AdmitWithDescendants(workerID, token, action, nil)
}

// AdmitWithDescendants is Admit for a mutation whose seam appends outbox rows.
// descendants are opaque keys naming those rows; on admission each key is
// bound to token, so Inherited hands a worker the spent chain rather than a
// fresh root when the row is delivered (ADR-0125: every descendant outbox event
// retains the token). A key that is already bound, to any chain, is refused
// with ErrDescendantCollision before anything is spent or recorded, so the
// caller can restamp its rows: a key never names rows of two mutations, and a
// chain can never pick up another chain's remaining budget. The ledger and the
// worker's share of it are checked before any budget is spent and fail closed
// when full.
func (c *CausationCap) AdmitWithDescendants(workerID string, token Token, action string, descendants []string) error {
	now := c.now().UTC()
	chainID, expiresAt, validationErr := c.validateToken(token, now)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	if validationErr != nil {
		c.recordAbortLocked(now, workerID, token, action, validationErr)
		return validationErr
	}
	if workerID == "" || action == "" {
		c.recordAbortLocked(now, workerID, token, action, ErrInvalidToken)
		return ErrInvalidToken
	}

	chain, tracked := c.chains[chainID]
	if tracked && chain.actions >= c.limits.GlobalActions {
		c.recordAbortLocked(now, workerID, token, action, ErrGlobalBudget)
		return ErrGlobalBudget
	}
	if !tracked && len(c.chains) >= c.limits.MaxTrackedChains {
		c.recordAbortLocked(now, workerID, token, action, ErrTrackingCapacity)
		return ErrTrackingCapacity
	}

	window, workerTracked := c.workers[workerID]
	if !workerTracked && len(c.workers) >= c.limits.MaxTrackedWorkers {
		c.recordAbortLocked(now, workerID, token, action, ErrTrackingCapacity)
		return ErrTrackingCapacity
	}
	if window.startedAt.IsZero() || !now.Before(window.startedAt.Add(c.limits.WorkerWindow)) {
		window = workerWindow{startedAt: now}
	}
	if window.actions >= c.limits.WorkerActions {
		c.recordAbortLocked(now, workerID, token, action, ErrWorkerRateLimit)
		return ErrWorkerRateLimit
	}
	keys := make(map[string]struct{}, len(descendants))
	for _, key := range descendants {
		if _, bound := c.descendants[key]; bound {
			return ErrDescendantCollision
		}
		keys[key] = struct{}{}
	}
	if len(c.descendants)+len(keys) > c.limits.MaxTrackedDescendants ||
		c.workerDescendants[workerID]+len(keys) > c.limits.MaxDescendantsPerWorker {
		c.recordAbortLocked(now, workerID, token, action, ErrTrackingCapacity)
		return ErrTrackingCapacity
	}
	// A descendant row is delivered after the mutation; its entry outlives the
	// row's own fresh-root lifetime, after which a re-minted root is expired.
	for key := range keys {
		c.descendants[key] = descendant{token: token, workerID: workerID, expiresAt: now.Add(c.limits.ChainLifetime)}
	}
	if len(keys) > 0 {
		c.workerDescendants[workerID] += len(keys)
	}

	chain.actions++
	chain.expiresAt = expiresAt
	window.actions++
	c.chains[chainID] = chain
	c.workers[workerID] = window
	return nil
}

// ReleaseDescendants unbinds keys that AdmitWithDescendants bound to token,
// for a mutation that was admitted but stopped before its seam ran, so it will
// append none of those rows. The spent budget is not refunded. Keys bound to
// another chain are left alone.
func (c *CausationCap) ReleaseDescendants(token Token, descendants []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range descendants {
		if entry, ok := c.descendants[key]; ok && entry.token == token {
			c.deleteDescendantLocked(key, entry)
		}
	}
}

func (c *CausationCap) deleteDescendantLocked(key string, entry descendant) {
	delete(c.descendants, key)
	if c.workerDescendants[entry.workerID] <= 1 {
		delete(c.workerDescendants, entry.workerID)
		return
	}
	c.workerDescendants[entry.workerID]--
}

// Inherited returns the chain token a descendant key was bound to by
// AdmitWithDescendants, or the zero Token when the key is unbound or expired.
func (c *CausationCap) Inherited(key string) Token {
	now := c.now().UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.descendants[key]
	if !ok || !now.Before(entry.expiresAt) {
		return ""
	}
	return entry.token
}

func (c *CausationCap) validateToken(token Token, now time.Time) ([sha256.Size]byte, time.Time, error) {
	chainID := sha256.Sum256([]byte(token))
	if c.entropyErr != nil {
		return chainID, time.Time{}, ErrInvalidToken
	}
	parts := strings.Split(string(token), ".")
	if len(parts) != 3 {
		return chainID, time.Time{}, ErrInvalidToken
	}
	startedMS, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return chainID, time.Time{}, ErrInvalidToken
	}
	rootDigest, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(rootDigest) != sha256.Size {
		return chainID, time.Time{}, ErrInvalidToken
	}
	suppliedMAC, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return chainID, time.Time{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(suppliedMAC, mac.Sum(nil)) {
		return chainID, time.Time{}, ErrInvalidToken
	}
	startedAt := time.UnixMilli(startedMS).UTC()
	if startedAt.After(now) {
		return chainID, time.Time{}, ErrInvalidToken
	}
	expiresAt := startedAt.Add(c.limits.ChainLifetime)
	if !now.Before(expiresAt) {
		return chainID, expiresAt, ErrChainExpired
	}
	return chainID, expiresAt, nil
}

func (c *CausationCap) pruneLocked(now time.Time) {
	for id, chain := range c.chains {
		if !now.Before(chain.expiresAt) {
			delete(c.chains, id)
		}
	}
	for workerID, window := range c.workers {
		if !now.Before(window.startedAt.Add(c.limits.WorkerWindow)) {
			delete(c.workers, workerID)
		}
	}
	for key, entry := range c.descendants {
		if !now.Before(entry.expiresAt) {
			c.deleteDescendantLocked(key, entry)
		}
	}
}

func (c *CausationCap) recordAbortLocked(now time.Time, workerID string, token Token, action string, err error) {
	reason := deadLetterReason(err)
	causationID := tokenFingerprint(token)
	for i := len(c.deadLetters) - 1; i >= 0; i-- {
		record := &c.deadLetters[i]
		if record.WorkerID != workerID || record.CausationID != causationID || record.Action != action || record.Reason != reason {
			continue
		}
		if now.Before(record.LastAt.Add(c.limits.DeadLetterWindow)) {
			record.LastAt = now
			record.Occurrences++
			return
		}
		break
	}

	if c.deadWindowStart.IsZero() || !now.Before(c.deadWindowStart.Add(c.limits.DeadLetterWindow)) {
		c.deadWindowStart = now
		c.deadWindowRecords = 0
	}
	if c.deadWindowRecords >= c.limits.DeadLetterRecords {
		c.suppressed++
		return
	}
	c.deadWindowRecords++
	c.deadLetters = append(c.deadLetters, DeadLetter{
		WorkerID: workerID, CausationID: causationID, Action: action, Reason: reason,
		FirstAt: now, LastAt: now, Occurrences: 1,
	})
	if len(c.deadLetters) > c.limits.DeadLetterCapacity {
		copy(c.deadLetters, c.deadLetters[len(c.deadLetters)-c.limits.DeadLetterCapacity:])
		c.deadLetters = c.deadLetters[:c.limits.DeadLetterCapacity]
		c.evicted++
	}
}

func deadLetterReason(err error) DeadLetterReason {
	switch {
	case errors.Is(err, ErrChainExpired):
		return DeadLetterExpiredChain
	case errors.Is(err, ErrGlobalBudget):
		return DeadLetterGlobalBudget
	case errors.Is(err, ErrWorkerRateLimit):
		return DeadLetterWorkerRate
	case errors.Is(err, ErrTrackingCapacity):
		return DeadLetterTrackingCapacity
	default:
		return DeadLetterInvalidToken
	}
}

func tokenFingerprint(token Token) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// DeadLetters returns a copy of the bounded observable surface.
func (c *CausationCap) DeadLetters() DeadLetterSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	records := make([]DeadLetter, len(c.deadLetters))
	copy(records, c.deadLetters)
	return DeadLetterSnapshot{Records: records, Suppressed: c.suppressed, Evicted: c.evicted}
}
