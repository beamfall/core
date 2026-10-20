package ffmpeg

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrPathSwapped is the fail-closed refusal every root-scoped check in this
// package returns. A generation op that ends in this error published nothing and
// removed its own temp: the destination is exactly as it was before the run.
var ErrPathSwapped = errors.New("ffmpeg: destination changed identity during the operation")

const outputTempMarker = ".beamfall-ffmpeg-p"

// outputTemp is a reserved output file bound to a descriptor on the directory it
// lives in. It replaces "make a temp path, hand it to FFmpeg, os.Rename it into
// place": that sequence names the destination directory three separate times, and
// each naming is an independent walk that can resolve somewhere new.
//
// Here the directory is opened ONCE. The temp is created through that descriptor,
// its inode is recorded, and the publish renames through the same descriptor
// after re-proving both that the directory is still the one that was opened and
// that the temp is still the file that was created. FFmpeg still writes by path —
// it is a separate process and takes a filename, not a descriptor — but it
// truncates the reserved file in place, so its output stays the inode the publish
// verifies. Anything that unlinks and re-creates that name mid-run (the classic
// post-check swap) changes the inode and is refused.
type outputTemp struct {
	root *os.Root
	// dir is the destination directory NAME, kept only to re-prove that the name
	// still resolves to the directory the descriptor holds.
	dir string
	// name is the temp's root-relative member name.
	name string
	// id is the identity of the file created at reservation time. The publish
	// refuses unless the member still names this exact file.
	id fs.FileInfo
	// held keeps the reserved inode allocated until the identity check. Without
	// it, an unlink-and-recreate can be handed the freed inode number (ext4 and
	// tmpfs reuse it at once), and os.SameFile would accept the substitute.
	held *os.File
	// Path is the absolute path handed to the FFmpeg child process.
	Path string
}

// reserveOutput opens the destination directory and reserves a fresh, uniquely
// named temp inside it. Two concurrent generations targeting the same dst get
// distinct temps, so neither truncates the other's in-progress output. The temp
// is created O_EXCL: openat refuses an existing entry — symlink included — so a
// pre-planted link can never become the file FFmpeg writes through.
func reserveOutput(dst string) (*outputTemp, error) {
	dir := filepath.Dir(dst)
	base := filepath.Base(dst)
	if dir == "" || base == "" || base == "." || base == string(filepath.Separator) {
		return nil, ErrPathSwapped
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening destination directory: %w", err)
	}
	if err := verifyDirUnreplaced(root, dir); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := recoverOutputTemps(root, dir, base); err != nil {
		_ = root.Close()
		return nil, err
	}
	ownerStart, err := ownerProcessStart(os.Getpid())
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("identifying temp owner: %w", err)
	}
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		_ = root.Close()
		return nil, err
	}
	name := base + outputTempMarker + strconv.Itoa(os.Getpid()) + "-" + ownerStart + "-" + hex.EncodeToString(token[:]) + ".tmp"
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	id, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = root.Remove(name)
		_ = root.Close()
		return nil, err
	}
	return &outputTemp{root: root, dir: dir, name: name, id: id, held: file, Path: filepath.Join(dir, name)}, nil
}

// recoverOutputTemps removes only dead-process reservations for this exact
// destination, through the already-verified destination root. PID and process
// start identity must both match before a reservation is considered live, so a
// reused PID cannot preserve the prior process incarnation's residue. Unrelated
// files are left untouched. Re-entering reserveOutput after a hard crash is the
// recovery trigger; repeated recovery is harmless.
func recoverOutputTemps(root *os.Root, dir, base string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading destination directory: %w", err)
	}
	prefix := base + outputTempMarker
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		ownerText, ownerSuffix, found := strings.Cut(strings.TrimPrefix(name, prefix), "-")
		if !found {
			continue
		}
		ownerPID, err := strconv.Atoi(ownerText)
		if err != nil || ownerPID <= 0 {
			continue
		}
		ownerStart, _, hasStart := strings.Cut(ownerSuffix, "-")
		if outputTempOwnerAlive(ownerPID, ownerStart, hasStart) {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func outputTempOwnerAlive(pid int, recordedStart string, hasStart bool) bool {
	if !ownerProcessAlive(pid) {
		return false
	}
	if !hasStart {
		return true
	}
	actualStart, err := ownerProcessStart(pid)
	if err != nil {
		return true
	}
	return actualStart == recordedStart
}

// publish atomically positions the reserved temp at dst's final component,
// through the SAME directory descriptor the temp was created in. It refuses when
// the directory name no longer resolves to that descriptor's directory (root
// replacement) or when the temp member no longer names the file that was reserved
// (final-component swap). On refusal the temp is removed and dst is untouched.
//
// The rename itself is the only step that can expose the new bytes under the
// destination name, and it runs after both proofs — so a caller that gets nil has
// published the file it produced, in the directory it reserved in, or nothing.
func (o *outputTemp) publish(dst string) error {
	defer o.close()
	if err := verifyDirUnreplaced(o.root, o.dir); err != nil {
		o.release()
		_ = o.root.Remove(o.name)
		return err
	}
	current, err := o.root.Lstat(o.name)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(o.id, current) {
		o.release()
		_ = o.root.Remove(o.name)
		return ErrPathSwapped
	}
	// Release before the rename: Windows refuses to rename a file with an open
	// handle, and the identity proof above no longer needs the pin.
	o.release()
	if err := o.root.Rename(o.name, filepath.Base(dst)); err != nil {
		_ = o.root.Remove(o.name)
		return err
	}
	return nil
}

// discard removes the reserved temp and releases the directory descriptor. It is
// the rollback and cancellation path: a failed, cancelled, or killed run leaves
// no artifact, and the removal is descriptor-relative, so it can only ever delete
// the member inside the directory that was reserved in — never a same-named file
// somewhere the directory name has since been re-pointed. A failed removal is
// returned without closing the root, leaving a subsequent discard able to retry.
func (o *outputTemp) discard() error {
	if o == nil || o.root == nil {
		return nil
	}
	o.release()
	if err := o.root.Remove(o.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing temp file: %w", err)
	}
	o.close()
	return nil
}

func (o *outputTemp) joinDiscardError(err *error) {
	discardErr := o.discard()
	if discardErr == nil {
		return
	}
	if *err == nil {
		*err = discardErr
		return
	}
	*err = errors.Join(*err, discardErr)
}

// release drops the reservation pin; the directory descriptor stays open.
func (o *outputTemp) release() {
	if o.held != nil {
		_ = o.held.Close()
		o.held = nil
	}
}

func (o *outputTemp) close() {
	o.release()
	if o.root != nil {
		_ = o.root.Close()
		o.root = nil
	}
}

// boundSource is an input file held open for the whole life of an FFmpeg run and
// bound to the directory entry it was opened from.
//
// The final component is Lstat'd, required to be regular, then opened and bound
// to that exact identity, so it can never silently become a symlink target. The
// descriptor remains held for the run, preventing identity reuse underneath the
// final verification.
type boundSource struct {
	file *os.File
	root *os.Root
	dir  string
	name string
	id   fs.FileInfo
}

// bindSource opens src as a stable reference for one generation run. A source
// that is not a regular file, whose final component is a symlink, or that lives
// under a directory name that does not resolve to the directory the descriptor
// holds, is refused before FFmpeg is launched.
func bindSource(src string) (*boundSource, error) {
	dir := filepath.Dir(src)
	name := filepath.Base(src)
	if dir == "" || name == "" || name == "." {
		return nil, ErrPathSwapped
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if err := verifyDirUnreplaced(root, dir); err != nil {
		_ = root.Close()
		return nil, err
	}
	entry, err := root.Lstat(name)
	if err != nil || !entry.Mode().IsRegular() {
		_ = root.Close()
		return nil, ErrPathSwapped
	}
	file, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	id, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = root.Close()
		return nil, err
	}
	if !id.Mode().IsRegular() || !os.SameFile(entry, id) {
		_ = file.Close()
		_ = root.Close()
		return nil, ErrPathSwapped
	}
	return &boundSource{file: file, root: root, dir: dir, name: name, id: id}, nil
}

// verify re-proves, after the run, that the source name still resolves to the
// file the run was authorised against and that its directory was not replaced. A
// mismatch means the input was swapped mid-run, so the output describes bytes the
// caller never approved and must not be published.
func (b *boundSource) verify() error {
	if err := verifyDirUnreplaced(b.root, b.dir); err != nil {
		return err
	}
	current, err := b.root.Lstat(b.name)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(b.id, current) {
		return ErrPathSwapped
	}
	return nil
}

func (b *boundSource) close() {
	if b.file != nil {
		_ = b.file.Close()
		b.file = nil
	}
	if b.root != nil {
		_ = b.root.Close()
		b.root = nil
	}
}

// verifyDirUnreplaced refuses a directory whose NAME no longer resolves to the
// directory the held descriptor references. os.SameFile is an inode-level
// identity comparison, so a directory that was renamed away, deleted, or replaced
// by another directory or a symlink to one all fail here; a directory reached
// through a STABLE symlink still passes, because both sides resolve to the same
// inode.
func verifyDirUnreplaced(root *os.Root, name string) error {
	held, err := root.Stat(".")
	if err != nil {
		return err
	}
	current, err := os.Stat(name) // #nosec G703 -- this operation must resolve the caller's directory name to compare it with the held root
	if err != nil {
		return err
	}
	if !os.SameFile(held, current) {
		return ErrPathSwapped
	}
	return nil
}
