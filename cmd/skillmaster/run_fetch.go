package main

// The fetch half of `run`: the manifest, the per-file stream, the cache marker, and the refusal
// for a version with no scripts. Split out of run.go when that file crossed the 1000-line guard;
// the decisions behind it are in ADR 0217 and the technical design.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lijinzhao86/skillmaster/skillmaster-cli/internal/api"
)

// runMarker names the local file that says which version a cache directory holds.
//
// **Not `SKILL.md`.** Since v78 the cache holds only `scripts/` (ADR 0217), so the old
// "the directory has SKILL.md" test would call every new directory a miss. The marker is the
// CLI's own file: it is not skill content, and a skill cannot forge it.
const runMarker = ".skillmaster-run.json"

type runMarkerFile struct {
	Digest string `json:"digest"`
	Rule   int    `json:"rule"`
}

// noScriptsError refuses a version with no executable part before anything is written.
type noScriptsError struct {
	name string
}

func (e noScriptsError) Error() string {
	return fmt.Sprintf("这一版没有可执行的脚本：%s 的 scripts/ 下没有文件。", e.name)
}

// fetchScripts is `run`'s half of the fetch: the manifest, then every file under `scripts/`.
//
// **The manifest is the contract, and each file is checked against it.** The list's ETag is the
// version digest this run resolved; a file whose bytes do not hash to the manifest's `sha256` stops
// the fetch before the staging directory is moved into place, so an executed script is always one
// the manifest described. The archive path (ADR 0068) is deliberately not used here: `run` must not
// need the tree it does not execute (ADR 0217).
func fetchScripts(ctx context.Context, base, digest, target string) error {
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
		return errors.New("服务端没有给出这一版的摘要，没法确认取到的是同一版；没有写入任何文件。")
	}
	if strings.TrimPrefix(served, "sha256:") != digest {
		return archiveMismatch{fmt.Sprintf(
			"服务端给的清单是 %s，不是要的 sha256:%s；没有写入任何文件，也没有运行它。",
			served, digest[:min(16, len(digest))])}
	}
	var scripts []api.File
	for _, file := range listed.Files {
		if strings.HasPrefix(file.Relpath, "scripts/") {
			scripts = append(scripts, file)
		}
	}
	if len(scripts) == 0 {
		return noScriptsError{name: base}
	}

	staging, err := stagingDir(target)
	if err != nil {
		return fmt.Errorf("创建临时目录：%w", err)
	}
	for _, file := range scripts {
		if err := fetchScriptFile(ctx, client, base, pin, staging, file); err != nil {
			return failedDownload(err, staging)
		}
	}
	if err := writeRunMarker(staging, digest); err != nil {
		return failedDownload(err, staging)
	}
	if err := place(staging, target, false); err != nil {
		return failedDownload(err, staging)
	}
	return nil
}

// fetchScriptFile streams one file into staging, checking the manifest's digest and size as it
// writes. Nothing is buffered whole: a script may be larger than the memory a run should hold.
func fetchScriptFile(ctx context.Context, client api.Client, base, pin, staging string,
	file api.File) error {
	// **The path arrived over a socket** (the archive path makes the same point in `unpack`): the
	// server validates what it stores, but a `..`, an absolute path or a backslash here would put
	// the write outside the staging directory, and the bytes are executable. Refusing before the
	// request costs one `Clean`; the prefix filter above is not a path check.
	rel := path.Clean(file.Relpath)
	if strings.ContainsRune(file.Relpath, '\\') || path.IsAbs(rel) || !strings.HasPrefix(rel, "scripts/") {
		return fmt.Errorf("清单里的路径 %q 不安全，已停止；没有写入最终目录。", file.Relpath)
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
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("写入 %s：%w", file.Relpath, err)
	}
	if err := out.Chmod(0o755); err != nil {
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
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
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

// writeRunMarker is written last: the rename is what makes the directory a hit, so the marker has to
// be inside it before the rename.
func writeRunMarker(dir, digest string) error {
	data, err := json.Marshal(runMarkerFile{Digest: "sha256:" + digest, Rule: cacheRuleVersion})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, runMarker), data, 0o644)
}

func hasRunMarker(dir, digest string) bool {
	data, err := os.ReadFile(filepath.Join(dir, runMarker))
	if err != nil {
		return false
	}
	var marker runMarkerFile
	if json.Unmarshal(data, &marker) != nil {
		return false
	}
	return marker.Digest == "sha256:"+digest && marker.Rule == cacheRuleVersion
}
