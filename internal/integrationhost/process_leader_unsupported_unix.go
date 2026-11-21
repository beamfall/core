//go:build unix && !darwin && !linux

package integrationhost

// Other Unix systems can neither observe the worker's exit without reaping
// it nor pin its group, so retire could signal a reused group ID. They are
// refused at build time, as internal/ffmpeg refuses them (CORE-2750).

func observeLeaderExit(int) error {
	return integrationhost_on_Unix_requires_Linux_or_macOS
}

func processGroupHasLiveMember(int) bool {
	return integrationhost_on_Unix_requires_Linux_or_macOS
}
