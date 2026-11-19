//go:build !linux

package ffmpeg

type hlsReaper struct{}

func (p *HLSProcess) start() error {
	applySysProcAttr(p.cmd)
	configureProcessGroup(p.cmd)
	return p.cmd.Start()
}

func (p *HLSProcess) terminateGroup() {
	killProcessGroup(p.cmd)
}

func (p *HLSProcess) waitAfterStop() {
	_ = p.Wait()
}
