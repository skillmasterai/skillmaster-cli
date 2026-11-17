package main

// The fetch half of `run`: the manifest, the contract's closure, the per-file stream, the run
// marker, and `--with`. Split out of run.go when that file crossed the 1000-line guard; the
// decisions behind it are in ADR 0222 and the technical design.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/config"
)

// runMarker names the local file that says which version a cache directory holds.
//
// **Not `SKILL.md`.** Since v78 the cache holds a closure rather than the whole version, so the old
// "the directory has SKILL.md" test would call every new directory a miss. The marker is the
// CLI's own file: it is not skill content, and a skill cannot forge it.
const runMarker = ".skillmaster-run.json"

// runMarkerFile is the hit test and the offline contract, in one file.
//
// **The contract is stored because a profile can change without the digest changing** (ADR 0222
// 后果). It is also what lets a machine with no network recompute the same cache directory name
// that the last online run used. `Extras` records `--with` paths so a later run can tell "already
// here" from "needs one more file".
type runMarkerFile struct {
	Digest       string           `json:"digest"`
	Rule         int              `json:"rule"`
	Contract     *api.RunContract `json:"contract,omitempty"`
	ContractHash string           `json:"contract_hash,omitempty"`
	Source       string           `json:"source,omitempty"`
	Extras       []string         `json:"extras,omitempty"`
}

// noScriptsError refuses a version with no executable part before anything is written.
type noScriptsError struct {
	name  string
	files []string
}

func (e noScriptsError) Error() string {
	return fmt.Sprintf("这一版没有可执行的脚本：%s 的运行契约（files: %s）在清单里没有命中任何文件。",
		e.name, strings.Join(e.files, "、"))
}

// fetchClosure is `run`'s half of the fetch: the manifest, then every file the contract delivers.
//
// **The manifest is the contract, and each file is checked against it.** The list's ETag is the
// version digest this run resolved; a file whose bytes do not hash to the manifest's `sha256` stops
// the fetch before the staging directory is moved into place, so an executed script is always one
// the manifest described. The archive path (ADR 0068) is deliberately not used here: `run` must not
// need the tree it does not execute (ADR 0222 §决定 3).
//
// **`--with` is fetched in the same pass.** Those paths are exact entries of the same manifest, so
// the version they are checked against is the one already being fetched; they land beside the
// closure and are recorded in the marker.
func fetchClosure(ctx context.Context, base, digest, target string, contract api.RunContract,
	source string, extras []string) error {
	client, err := session(ctx)
	if err != nil {
		return err
	}
	pin := "sha256:" + digest
	listed, err := client.AuthorFileList(ctx, base, pin)
	if err != nil {
		return versionRefusal(err)
	}
	// **The manifest has to say which version it is.** A missing or malformed ETag is not "no
	// opinion": without it the per-file hashes bind the bytes to this listing only, not to the
	// digest this run resolved and recorded as consent. `check` already refuses the same gap, and
	// two commands reading one route must not disagree about whether an answer is trustworthy.
	served := digestPinOf(listed.ETag)
	if served == "" {
		return fmt.Errorf("服务端没有给出这一版的摘要，没法确认取到的是同一版；没有写入任何文件。")
	}
	if strings.TrimPrefix(served, "sha256:") != digest {
		return archiveMismatch{fmt.Sprintf(
			"服务端给的清单是 %s，不是要的 sha256:%s；没有写入任何文件，也没有运行它。",
			served, digest[:min(16, len(digest))])}
	}
	// **A directory named for PATH or PYTHONPATH is part of the closure** (ADR 0222): declaring
	// `path: ["bin"]` without also repeating `bin/` in `files` used to inject a directory the
	// process never received. Same rule as the server's `closure()`.
	declarations := append(append(append([]string{}, contract.Files...), contract.Path...),
		contract.Pythonpath...)
	closure := contractClosure(listed.Files, declarations)
	if len(closure) == 0 {
		return noScriptsError{name: base, files: declarations}
	}
	extraFiles, err := manifestEntries(listed.Files, extras, base)
	if err != nil {
		return err
	}

	staging, err := stagingDir(target)
	if err != nil {
		return fmt.Errorf("创建临时目录：%w", err)
	}
	delivered := map[string]bool{}
	for _, file := range closure {
		delivered[path.Clean(file.Relpath)] = true
		if err := fetchContractFile(ctx, client, base, pin, staging, file, executablePrefixes(contract)); err != nil {
			return failedDownload(err, staging)
		}
	}
	for _, file := range extraFiles {
		if delivered[path.Clean(file.Relpath)] {
			continue
		}
		delivered[path.Clean(file.Relpath)] = true
		if err := fetchContractFile(ctx, client, base, pin, staging, file, executablePrefixes(contract)); err != nil {
			return failedDownload(err, staging)
		}
	}
	if err := writeRunMarker(staging, digest, contract, source, extras); err != nil {
		return failedDownload(err, staging)
	}
	if err := place(staging, target, false); err != nil {
		return failedDownload(err, staging)
	}
	return nil
}

// manifestEntries resolves exact `--with` paths against the manifest.
//
// **Only a path the version contains.** A spelled path that is absent is refused before any
// request: `run` does not fetch by name, and a caller asking for a file the version does not ship
// has made a typo or is reaching outside the package.
func manifestEntries(files []api.File, extras []string, base string) ([]api.File, error) {
	if len(extras) == 0 {
		return nil, nil
	}
	byPath := make(map[string]api.File, len(files))
	for _, file := range files {
		byPath[path.Clean(file.Relpath)] = file
	}
	out := make([]api.File, 0, len(extras))
	for _, extra := range extras {
		if err := validateWithPath(extra); err != nil {
			return nil, err
		}
		clean := path.Clean(extra)
		file, found := byPath[clean]
		if !found {
			return nil, fmt.Errorf("--with 的路径 %q 不在 %s 这一版的清单里：只能补这一版已有的文件",
				extra, base)
		}
		out = append(out, file)
	}
	return out, nil
}

// ensureWithFiles satisfies `--with` when the cache already holds this version.
//
// **Reuse before asking.** A path recorded by an earlier run is a plain file in the directory and
// needs no request; only a path this directory has never held is fetched, by the single-file read
// the technical design names (ADR 0222 接口), and its bytes are checked against the manifest just
// as the closure's are.
func ensureWithFiles(ctx context.Context, base, digest, dir string, extras []string,
	executable []string) error {
	if len(extras) == 0 {
		return nil
	}
	var missing []string
	for _, extra := range extras {
		if err := validateWithPath(extra); err != nil {
			return err
		}
		clean := path.Clean(extra)
		if !isFile(filepath.Join(dir, filepath.FromSlash(clean))) {
			missing = append(missing, clean)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	client, err := session(ctx)
	if err != nil {
		return err
	}
	pin := "sha256:" + digest
	listed, err := client.AuthorFileList(ctx, base, pin)
	if err != nil {
		return versionRefusal(err)
	}
	served := digestPinOf(listed.ETag)
	if strings.TrimPrefix(served, "sha256:") != digest {
		return archiveMismatch{fmt.Sprintf(
			"服务端给的清单是 %s，不是要的 sha256:%s；没有写入任何文件，也没有运行它。",
			served, digest[:min(16, len(digest))])}
	}
	files, err := manifestEntries(listed.Files, missing, base)
	if err != nil {
		return err
	}
	for _, file := range files {
		body, err := client.AuthorFile(ctx, base, pin, file.Relpath)
		if err != nil {
			return err
		}
		if err := writeContractBytes(dir, file, body, executable); err != nil {
			return err
		}
	}
	// **Record what is now here.** Best effort: the files are the fact, and the marker only saves a
	// later run from looking them up again.
	if marker, ok := readRunMarker(dir); ok {
		marker.Extras = appendUnique(marker.Extras, extras...)
		if data, err := json.Marshal(marker); err == nil {
			_ = writeFileAtomic(filepath.Join(dir, runMarker), data, 0o644)
		}
	}
	return nil
}

// refuseSymlinkedPath refuses a destination whose existing components under `root` include a
// symlink. `root` itself is the private cache directory; nothing needs to be a link inside it.
func refuseSymlinkedPath(root, dest string) error {
	relative, err := filepath.Rel(root, dest)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("拒绝写入：%s 不在缓存目录里", dest)
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝写入 %s：路径里 %s 是一个符号链接", dest, current)
		}
	}
	return nil
}

// writeFileAtomic replaces a file through a temporary sibling and a rename.
//
// **A killed run must leave the old bytes or the new bytes, never half of either.** The cache is
// addressed by digest and a later run treats an existing path as complete, so a truncated
// `--with` file or marker would be trusted forever (v82 audit).
func writeFileAtomic(dest string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(dest)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	// No cleanup on failure: the CLI never deletes files (ADR 0101). A failed write leaves its
	// temporary sibling in the same directory, where nothing reads it as a closure file.
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dest)
}

// fetchContractFile streams one file into staging, checking the manifest's digest and size as it
// writes. Nothing is buffered whole: a file may be larger than the memory a run should hold.
func fetchContractFile(ctx context.Context, client api.Client, base, pin, staging string,
	file api.File, executable []string) error {
	rel, err := safeRelpath(file.Relpath)
	if err != nil {
		return err
	}
	dest := filepath.Join(staging, filepath.FromSlash(rel))
	if !inside(staging, dest) {
		return fmt.Errorf("清单里的路径 %q 不安全，已停止；没有写入最终目录。", file.Relpath)
	}
	stream, err := client.StreamFile(ctx, authorFilePath(base, pin, rel), "", "")
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("创建目录：%w", err)
	}
	mode := relpathMode(rel, executable)
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("写入 %s：%w", file.Relpath, err)
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return fmt.Errorf("设置 %s 的权限：%w", file.Relpath, err)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, hash), stream)
	if err != nil {
		out.Close()
		return fmt.Errorf("读取 %s：%w", file.Relpath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("写入 %s：%w", file.Relpath, err)
	}
	want := strings.TrimPrefix(file.SHA256, "sha256:")
	if want == "" {
		return fmt.Errorf("服务端的清单没有给出 %s 的摘要，没法确认这一版；没有写入最终目录。",
			file.Relpath)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		return archiveMismatch{fmt.Sprintf(
			"%s 的字节与清单不符（清单 %s，实际 %s）；没有写入最终目录，也没有运行它。",
			file.Relpath, want[:min(16, len(want))], got[:min(16, len(got))])}
	}
	if written != file.Size {
		return fmt.Errorf("%s 的长度与清单不符（清单 %d 字节，实际 %d）；没有写入最终目录。",
			file.Relpath, file.Size, written)
	}
	return nil
}

// writeContractBytes places one `--with` read after checking it against the manifest.
func writeContractBytes(dir string, file api.File, body []byte, executable []string) error {
	rel, err := safeRelpath(file.Relpath)
	if err != nil {
		return err
	}
	dest := filepath.Join(dir, filepath.FromSlash(rel))
	if !inside(dir, dest) {
		return fmt.Errorf("清单里的路径 %q 不安全，已停止；没有写入。", file.Relpath)
	}
	want := strings.TrimPrefix(file.SHA256, "sha256:")
	if want == "" {
		return fmt.Errorf("服务端的清单没有给出 %s 的摘要，没法确认这一版；没有写入。", file.Relpath)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != want {
		return archiveMismatch{fmt.Sprintf(
			"%s 的字节与清单不符（清单 %s，实际 %s）；没有写入，也没有运行它。",
			file.Relpath, want[:min(16, len(want))], got[:min(16, len(got))])}
	}
	if int64(len(body)) != file.Size {
		return fmt.Errorf("%s 的长度与清单不符（清单 %d 字节，实际 %d）；没有写入。",
			file.Relpath, file.Size, len(body))
	}
	// The lexical `inside` check above cannot see a symlink inside the cache; a `--with` write
	// must not follow one out of it (v82 audit).
	if err := refuseSymlinkedPath(dir, dest); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("创建目录：%w", err)
	}
	mode := relpathMode(rel, executable)
	if err := writeFileAtomic(dest, body, mode); err != nil {
		return fmt.Errorf("写入 %s：%w", file.Relpath, err)
	}
	if err := os.Chmod(dest, mode); err != nil {
		return fmt.Errorf("设置 %s 的权限：%w", file.Relpath, err)
	}
	return nil
}

// safeRelpath refuses a manifest path that would escape the directory being written.
//
// **These bytes arrived over a socket.** The server validates what it stores, but a `..`, an
// absolute path or a backslash here would put the write outside the staging directory, and the
// bytes are executable.
func safeRelpath(relpath string) (string, error) {
	rel := path.Clean(relpath)
	if strings.ContainsRune(relpath, '\\') || path.IsAbs(rel) || rel == "." ||
		rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("清单里的路径 %q 不安全，已停止；没有写入最终目录。", relpath)
	}
	return rel, nil
}

// relpathMode is the executable bit's rule (ADR 0166 §决定 1): `scripts/` is 0755, everything else
// is 0644. A closure file outside `scripts/` is data — a template, a theme — and marking it
// executable would be a fact the version did not declare.
func relpathMode(relpath string, executable []string) os.FileMode {
	if strings.HasPrefix(relpath, "scripts/") {
		return 0o755
	}
	for _, prefix := range executable {
		if strings.HasPrefix(relpath, prefix) {
			return 0o755
		}
	}
	return 0o644
}

// executablePrefixes is ADR 0166's rule extended by the contract (v82): `scripts/` is executable,
// and so is a directory the version declares in `path` — that field exists to make its tools
// callable by name, and a 0644 tool cannot be executed.
func executablePrefixes(contract api.RunContract) []string {
	out := []string{"scripts/"}
	for _, dir := range contract.Path {
		if dir == "" {
			continue
		}
		if strings.HasSuffix(dir, "/") {
			out = append(out, dir)
		} else {
			out = append(out, dir+"/")
		}
	}
	return out
}

// writeRunMarker is written last: the rename is what makes the directory a hit, so the marker has to
// be inside it before the rename.
func writeRunMarker(dir, digest string, contract api.RunContract, source string,
	extras []string) error {
	stored := contract
	data, err := json.Marshal(runMarkerFile{
		Digest:       "sha256:" + digest,
		Rule:         cacheRuleVersion,
		Contract:     &stored,
		ContractHash: contractHash(contract),
		Source:       source,
		Extras:       appendUnique(nil, extras...),
	})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, runMarker), data, 0o644)
}

func hasRunMarker(dir, digest, contractHash string) bool {
	marker, ok := readRunMarker(dir)
	if !ok {
		return false
	}
	return marker.Digest == "sha256:"+digest && marker.Rule == cacheRuleVersion &&
		marker.ContractHash == contractHash
}

func readRunMarker(dir string) (runMarkerFile, bool) {
	data, err := os.ReadFile(filepath.Join(dir, runMarker))
	if err != nil {
		return runMarkerFile{}, false
	}
	var marker runMarkerFile
	if json.Unmarshal(data, &marker) != nil {
		return runMarkerFile{}, false
	}
	return marker, true
}

// cachedRunContract is the contract this machine last fetched for this digest.
//
// **The offline answer to a question only the server can answer.** The directory's own marker holds
// the contract it was written under, so a run with no server can still recompute the same directory
// name and reuse the bytes. When several contracts exist for one digest, the first readable one is
// used: they were all fetched for this digest, and any of them is a local answer.
func cachedRunContract(cacheIn, digest string) (api.RunContract, string, bool) {
	root := cacheIn
	if root == "" {
		root = filepath.Join(os.TempDir(), config.Name, "cache")
	}
	// **The same ownership check the materializer makes**, for a requested root too: this read
	// bypasses `runCacheRoot`, so without it a contract planted in a shared directory (with its own
	// `env`, `PATH`, `PYTHONPATH`) would reach the child although the bytes are re-fetched privately.
	if err := privateCacheRoot(root); err != nil {
		return api.RunContract{}, "", false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return api.RunContract{}, "", false
	}
	prefix := fmt.Sprintf("%s-r%d-c", cacheKey(digest), cacheRuleVersion)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		marker, ok := readRunMarker(filepath.Join(root, entry.Name()))
		if !ok || marker.Digest != "sha256:"+digest || marker.Rule != cacheRuleVersion ||
			marker.Contract == nil {
			continue
		}
		contract, err := checkRunContract(*marker.Contract)
		if err != nil {
			continue
		}
		source := marker.Source
		if source == "" {
			source = "inferred"
		}
		return contract, source, true
	}
	return api.RunContract{}, "", false
}

func appendUnique(list []string, values ...string) []string {
	seen := make(map[string]bool, len(list))
	for _, value := range list {
		seen[value] = true
	}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		list = append(list, value)
	}
	return list
}
