package main

// The v82 run-contract tests: command resolution by shebang, the three cwd modes, the contract's
// closure, `--explain` and `--with`. Pure functions are tested directly; the end-to-end cases follow
// the existing `pointAtServer` + `runCaptured` pattern and never touch the network.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
)

func writeSkillFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return full
}

func defaultContractJSON() string {
	return `{"schemaVersion":1,"files":["scripts/"],"path":[],"pythonpath":[],` +
		`"cwd":"script","env":{},"limits":{"timeoutSeconds":120},"default":null}`
}

// **An extensionless launcher can be the first word** — the failure that motivated v82 (Impeccable).
// The bare word is not on PATH, it is a real file in the closure, and its first line is `#!`.
func TestRunResolvesAShebangLauncherAsTheFirstWord(t *testing.T) {
	root := t.TempDir()
	launcher := writeSkillFile(t, root, "bin/impeccable", "#!/bin/sh\nexit 0\n")

	resolved, script, err := resolveRunCommand(root, []string{"impeccable", "context"})
	if err != nil {
		t.Fatalf("resolveRunCommand: %v", err)
	}
	if resolved[0] != launcher {
		t.Errorf("first word = %q, want %q", resolved[0], launcher)
	}
	if script != launcher {
		t.Errorf("script = %q, want %q (cwd: script uses it)", script, launcher)
	}
	if resolved[1] != "context" {
		t.Errorf("a data argument was rewritten to %q", resolved[1])
	}
}

// **The closure must not shadow a system command.** A package that ships a file named `sh`, `git`
// or `ls` still runs the system one when the bare word is on PATH (ADR 0222 §决定 4).
func TestRunLeavesABareFirstWordThatPathFinds(t *testing.T) {
	root := t.TempDir()
	// The file exists in the closure and has a shebang; only PATH membership keeps it from winning.
	writeSkillFile(t, root, "sh", "#!/bin/sh\necho shadowed\n")

	resolved, script, err := resolveRunCommand(root, []string{"sh", "-c", "true"})
	if err != nil {
		t.Fatalf("resolveRunCommand: %v", err)
	}
	if resolved[0] != "sh" {
		t.Errorf("first word was shadowed by the closure: %q", resolved[0])
	}
	if script != "" {
		t.Errorf("script = %q, want none: nothing in the command was resolved", script)
	}
}

// **A spelled path is the caller's own.** `./impeccable` means this file even if some `impeccable`
// is on PATH.
func TestRunResolvesASpelledFirstWordInsideTheClosure(t *testing.T) {
	root := t.TempDir()
	launcher := writeSkillFile(t, root, "impeccable", "#!/bin/sh\nexit 0\n")

	resolved, _, err := resolveRunCommand(root, []string{"./impeccable", "context"})
	if err != nil {
		t.Fatalf("resolveRunCommand: %v", err)
	}
	if len(resolved) == 0 {
		t.Fatal("empty command")
	}
	if resolved[0] != launcher {
		t.Errorf("first word = %q, want %q", resolved[0], launcher)
	}

	// A spelled launcher that is not in the closure is a refusal, with the closure listed.
	if _, _, err := resolveRunCommand(root, []string{"./nope"}); err == nil {
		t.Fatal("a missing ./word was passed through")
	} else if !strings.Contains(err.Error(), "现有") {
		t.Errorf("the refusal does not list the closure: %v", err)
	}
}

// **An extensionless data argument is not a script.** `--input data` names a value, not a launcher;
// only a shebang makes an extensionless file executable, and a data file does not have one.
func TestRunLeavesExtensionlessDataArgumentsAlone(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "data", "a,b\n1,2\n")
	writeSkillFile(t, root, "notes.txt", "hello\n")

	resolved, _, err := resolveRunCommand(root, []string{"python3", "-c", "print(1)", "data", "notes.txt"})
	if err != nil {
		t.Fatalf("resolveRunCommand: %v", err)
	}
	if resolved[0] != "python3" || resolved[2] != "print(1)" {
		t.Errorf("script tokens were changed: %v", resolved)
	}
	if resolved[3] != "data" || resolved[4] != "notes.txt" {
		t.Errorf("data arguments were rewritten: %v", resolved)
	}
}

// An extensionless *shebang* file as a later argument is a script and is resolved.
func TestRunResolvesALaterShebangArgument(t *testing.T) {
	root := t.TempDir()
	launcher := writeSkillFile(t, root, "bin/tool", "#!/usr/bin/env sh\nexit 0\n")

	resolved, _, err := resolveRunCommand(root, []string{"sh", "bin/tool"})
	if err != nil {
		t.Fatalf("resolveRunCommand: %v", err)
	}
	if resolved[1] != launcher {
		t.Errorf("bin/tool = %q, want %q", resolved[1], launcher)
	}
}

// The contract's three cwd modes, and a relative `--cwd` measured from the caller's directory.
func TestRunWorkingDirectoryModes(t *testing.T) {
	skill := t.TempDir()
	caller := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skill, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(caller, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(skill, "scripts", "plan.py")

	if got, err := workingDirectory(skill, "script", script, "", caller); err != nil ||
		got != filepath.Join(skill, "scripts") {
		t.Errorf("script mode = %q (%v)", got, err)
	}
	if got, err := workingDirectory(skill, "package", script, "", caller); err != nil || got != skill {
		t.Errorf("package mode = %q (%v)", got, err)
	}
	if got, err := workingDirectory(skill, "project", script, "", caller); err != nil || got != caller {
		t.Errorf("project mode = %q (%v)", got, err)
	}
	// **A relative --cwd is the caller's path**, not the version root's.
	if got, err := workingDirectory(skill, "package", "", "sub", caller); err != nil ||
		got != filepath.Join(caller, "sub") {
		t.Errorf("relative --cwd = %q (%v), want %q", got, err, filepath.Join(caller, "sub"))
	}
	if _, err := workingDirectory(skill, "package", "", "missing", caller); err == nil {
		t.Error("a --cwd that does not exist was accepted")
	}
}

// `--with` may only name a relative path inside the version, and only one the manifest lists.
func TestRunWithRejectsPathsOutsideTheVersion(t *testing.T) {
	for _, bad := range []string{"", "../x", "a/../../b", "/etc/passwd", ".."} {
		if err := validateWithPath(bad); err == nil {
			t.Errorf("%q was accepted by --with", bad)
		}
	}
	for _, good := range []string{"reference/a.md", "templates/x.yaml"} {
		if err := validateWithPath(good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
	files := []api.File{{Relpath: "scripts/x.sh", Size: 1}}
	if _, err := manifestEntries(files, []string{"reference/missing.md"}, "dev/x"); err == nil {
		t.Error("a path the manifest does not list was accepted")
	} else if !strings.Contains(err.Error(), "清单") {
		t.Errorf("the refusal does not name the manifest: %v", err)
	}
	if got, err := manifestEntries(files, []string{"scripts/x.sh"}, "dev/x"); err != nil || len(got) != 1 {
		t.Errorf("a listed path was refused: %v (%v)", got, err)
	}
}

// **The fetch range is the contract's closure.** `reference/` lands, `themes/` and `SKILL.md` do not,
// and the cache directory name carries the contract hash.
func TestRunFetchesTheContractsClosure(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a1", 32)
	files := map[string]string{
		"SKILL.md":        "---\nname: hello\ndescription: d\n---\n",
		"scripts/run.sh":  "#!/bin/sh\necho closure-ok\n",
		"reference/a.md":  "reference",
		"themes/t.css":    "body{}",
		"unrelated/b.bin": "\x00\x01",
	}
	contract := api.RunContract{
		SchemaVersion: 1,
		Files:         []string{"scripts/", "reference/"},
		Cwd:           "script",
		Limits:        api.RunLimits{TimeoutSeconds: 120},
	}
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":` + string(contractJSON) +
				`,"source":"package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			if r.Header.Get(api.VersionHeader) != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	stdout, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "sh", "run.sh")
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	if !strings.Contains(stdout, "closure-ok") {
		t.Errorf("the script did not run: %q", stdout)
	}
	dir := filepath.Join(cache, cacheDirName(digest, contractHash(contract)))
	if !isFile(filepath.Join(dir, "scripts", "run.sh")) {
		t.Error("scripts/run.sh is missing from the cache")
	}
	if !isFile(filepath.Join(dir, "reference", "a.md")) {
		t.Error("reference/a.md was not fetched, but the contract declares it")
	}
	for _, absent := range []string{"themes/t.css", "unrelated/b.bin", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(absent))); !os.IsNotExist(err) {
			t.Errorf("%s should not be in the closure (err = %v)", absent, err)
		}
	}
	// ADR 0166: the executable bit follows the path.
	info, err := os.Stat(filepath.Join(dir, "reference", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("a closure file outside scripts/ is %v, want 0644", info.Mode().Perm())
	}
}

// **`PATH` and `PYTHONPATH` are the contract's, resolved inside the version root.**
func TestRunInjectsTheContractsPathAndPythonpath(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a2", 32)
	files := map[string]string{
		"scripts/echo.sh": "#!/bin/sh\necho \"path=$PATH\"\necho \"python=$PYTHONPATH\"\n",
		"bin/tool":        "#!/bin/sh\n",
		"lib/pkg.py":      "#\n",
	}
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":{"schemaVersion":1,` +
				`"files":["scripts/","bin/","lib/"],"path":["bin"],"pythonpath":["lib"],` +
				`"cwd":"script","env":{},"limits":{"timeoutSeconds":120},"default":null},` +
				`"source":"package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			if r.Header.Get(api.VersionHeader) != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	stdout, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", cache, "--", "sh", "echo.sh")
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	dir := filepath.Join(cache, cacheDirName(digest, contractHash(api.RunContract{
		SchemaVersion: 1, Files: []string{"scripts/", "bin/", "lib/"}, Path: []string{"bin"},
		Pythonpath: []string{"lib"}, Cwd: "script",
		Limits: api.RunLimits{TimeoutSeconds: 120}})))
	if !strings.Contains(stdout, filepath.Join(dir, "bin")) {
		t.Errorf("PATH does not start with the contract's directory: %q", stdout)
	}
	if !strings.Contains(stdout, filepath.Join(dir, "lib")) {
		t.Errorf("PYTHONPATH does not start with the contract's directory: %q", stdout)
	}
}

// **`--explain` reports the contract and its source without fetching or running anything.**
func TestRunExplainReportsTheContractAndClosure(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a3", 32)
	files := map[string]string{
		"scripts/run.sh": "#!/bin/sh\necho should-not-run\n",
		"reference/a.md": "0123456789",
	}
	fileRequests := 0
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":{"schemaVersion":1,` +
				`"files":["scripts/","reference/"],"path":[],"pythonpath":[],` +
				`"cwd":"package","env":{},"limits":{"timeoutSeconds":90},"default":null},` +
				`"source":"profile","problems":["包内 run.json 有未知字段"]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			if r.Header.Get(api.VersionHeader) != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			var rows []string
			for relpath, body := range files {
				rows = append(rows, fmt.Sprintf(
					`{"relpath":%q,"sha256":"sha256:%s","size":%d,"is_binary":false}`,
					relpath, strings.Repeat("00", 32), len(body)))
			}
			_, _ = w.Write([]byte(`{"files":[` + strings.Join(rows, ",") + `]}`))
		case strings.Contains(r.URL.Path, "/files/"):
			fileRequests++
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	stdout, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--explain", "--json")
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	if fileRequests != 0 {
		t.Errorf("--explain fetched %d files; it must fetch none", fileRequests)
	}
	if strings.Contains(stdout, "should-not-run") {
		t.Errorf("--explain executed the command: %q", stdout)
	}
	var answer struct {
		Source   string          `json:"source"`
		Contract api.RunContract `json:"contract"`
		Closure  explainClosure  `json:"closure"`
		CwdMode  string          `json:"cwd_mode"`
		Problems []string        `json:"problems"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answer); err != nil {
		t.Fatalf("--explain --json is not JSON: %v (%q)", err, stdout)
	}
	if answer.Source != "profile" {
		t.Errorf("source = %q, want profile", answer.Source)
	}
	if answer.Closure.Files != 2 || answer.Closure.Bytes != int64(len(files["scripts/run.sh"])+len(files["reference/a.md"])) {
		t.Errorf("closure = %+v", answer.Closure)
	}
	if answer.CwdMode != "package" {
		t.Errorf("cwd_mode = %q", answer.CwdMode)
	}
	if len(answer.Problems) != 1 {
		t.Errorf("problems = %v, want the server's note", answer.Problems)
	}

	// The human form names the source in the PRD's words; `package` is printed as `in-package`.
	text, _, status := runCaptured(t, "run", "me/hello@JD0T518T", "--explain")
	if status != 0 {
		t.Fatalf("text explain status = %d", status)
	}
	if !strings.Contains(text, "来源 profile") || !strings.Contains(text, "闭包：2 个文件") {
		t.Errorf("text explain does not report the contract: %q", text)
	}
}

func TestRunExplainPrintsInPackage(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a4", 32)
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":` + defaultContractJSON() +
				`,"source":"package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			if r.Header.Get(api.VersionHeader) != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			_, _ = w.Write([]byte(`{"files":[{"relpath":"scripts/run.sh",` +
				`"sha256":"sha256:` + strings.Repeat("00", 32) + `","size":1,"is_binary":false}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	text, _, status := runCaptured(t, "run", "me/hello@JD0T518T", "--explain")
	if status != 0 {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(text, "来源 in-package") {
		t.Errorf("the PRD's word for a package contract is not printed: %q", text)
	}
}

// **`--with` brings in one manifest path beyond the closure, and a later run reuses it.**
func TestRunWithFetchesOneManifestPathAndReusesIt(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a5", 32)
	files := map[string]string{
		"scripts/read.sh": "#!/bin/sh\ncat \"$(dirname \"$0\")/../reference/a.md\"\n",
		"reference/a.md":  "with-content\n",
	}
	referenceFetches := 0
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":` + defaultContractJSON() +
				`,"source":"inferred","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			if r.Header.Get(api.VersionHeader) != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
			if strings.Contains(r.URL.Path, "/files/reference/") {
				referenceFetches++
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	cache := t.TempDir()
	for attempt := 1; attempt <= 2; attempt++ {
		stdout, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
			"--cache-in", cache, "--with", "reference/a.md", "--", "sh", "read.sh")
		if status != 0 {
			t.Fatalf("run %d: status = %d, stderr = %q", attempt, status, stderr)
		}
		if !strings.Contains(stdout, "with-content") {
			t.Errorf("run %d: --with file was not there: %q", attempt, stdout)
		}
	}
	if referenceFetches != 1 {
		t.Errorf("reference/a.md fetched %d times, want 1 (the second run reuses it)", referenceFetches)
	}
}

// A `--with` path the version does not contain is refused before anything runs.
func TestRunWithRefusesAPathNotInTheManifest(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a6", 32)
	files := map[string]string{"scripts/run.sh": "#!/bin/sh\necho ran\n"}
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			if r.Header.Get(api.VersionHeader) != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
		default:
			http.NotFound(w, r)
		}
	})

	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", t.TempDir(), "--with", "reference/missing.md", "--", "sh", "run.sh")
	if status == 0 {
		t.Fatalf("a path outside the manifest was accepted; stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "清单") {
		t.Errorf("the refusal does not name the manifest: %q", stderr)
	}
}

// **`limits.timeoutSeconds` terminates the run and answers 124** (PRD A11).
func TestRunStopsAtTheContractsDeadline(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a7", 32)
	files := map[string]string{"scripts/slow.sh": "#!/bin/sh\nsleep 5\n"}
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":{"schemaVersion":1,"files":["scripts/"],` +
				`"path":[],"pythonpath":[],"cwd":"script","env":{},` +
				`"limits":{"timeoutSeconds":1},"default":null},"source":"package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			if r.Header.Get(api.VersionHeader) != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"nope"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
		default:
			http.NotFound(w, r)
		}
	})

	_, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", t.TempDir(), "--", "sh", "slow.sh")
	if status != 124 {
		t.Fatalf("status = %d, want 124; stderr = %q", status, stderr)
	}
	if !strings.Contains(stderr, "执行超时") {
		t.Errorf("the timeout was not named: %q", stderr)
	}
}

// **The contract's `default` is the command when the caller names none** (ADR 0222 §决定 1).
func TestRunUsesTheContractsDefaultWhenNoCommandIsGiven(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("b3", 32)
	files := map[string]string{"scripts/plan.sh": "#!/bin/sh\necho plan\n"}
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":{"schemaVersion":1,"files":["scripts/"],` +
				`"path":[],"pythonpath":[],"cwd":"script","env":{},` +
				`"limits":{"timeoutSeconds":10},"default":["echo","hello-from-default"]},` +
				`"source":"package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case runFetchStub(t, w, r, digest, files):
		default:
			http.NotFound(w, r)
		}
	})

	stdout, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--yes",
		"--cache-in", t.TempDir())
	if status != 0 {
		t.Fatalf("status = %d, want 0; stderr = %q", status, stderr)
	}
	if !strings.Contains(stdout, "hello-from-default") {
		t.Errorf("the default did not run: stdout = %q, stderr = %q", stdout, stderr)
	}
}

// **An invalid contract fails closed**, and the refusal names the field (v82 Q5).
func TestRunRefusesAnInvalidContract(t *testing.T) {
	for _, row := range []struct {
		name string
		json string
		want string
	}{
		{"schema", `{"schemaVersion":2}`, "schemaVersion"},
		{"climb", `{"schemaVersion":1,"files":["../etc/"]}`, "files"},
		{"cwd", `{"schemaVersion":1,"cwd":"elsewhere"}`, "cwd"},
		{"reserved", `{"schemaVersion":1,"env":{"SKILLMASTER_SKILL_DIR":"/x"}}`, "SKILLMASTER_"},
	} {
		t.Run(row.name, func(t *testing.T) {
			var contract api.RunContract
			if err := json.Unmarshal([]byte(row.json), &contract); err != nil {
				t.Fatal(err)
			}
			if _, err := checkRunContract(contract); err == nil {
				t.Fatalf("%s was accepted", row.name)
			} else if !strings.Contains(err.Error(), row.want) {
				t.Errorf("the refusal does not name %q: %v", row.want, err)
			}
		})
	}
}

// **The inferred default is the pre-v82 behaviour**, and it hashes the same as a server that spells
// the same contract with empty arrays and an empty map.
func TestRunInferredContractHashesLikeTheServersDefault(t *testing.T) {
	serverDefault := api.RunContract{
		SchemaVersion: 1, Files: []string{"scripts/"},
		Path: []string{}, Pythonpath: []string{}, Cwd: "script",
		Env: map[string]string{}, Limits: api.RunLimits{TimeoutSeconds: 120},
	}
	if contractHash(serverDefault) != contractHash(inferredContract()) {
		t.Errorf("the inferred default and the server's default hash differently: %s vs %s",
			contractHash(inferredContract()), contractHash(serverDefault))
	}
}

// `--explain` answers `cwd: script` from the contract's own `default`, so a bare `skmr run`
// reports the directory it would actually use — not the version root it would only fall back to
// when there is no command at all.
func TestRunExplainResolvesScriptCwdFromDefault(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a5", 32)
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":{"schemaVersion":1,` +
				`"files":["scripts/"],"path":[],"pythonpath":[],"cwd":"script",` +
				`"env":{},"limits":{"timeoutSeconds":120},` +
				`"default":["impeccable","context"]},"source":"in-package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/launcher/versions/V1"):
			_, _ = w.Write([]byte(`{"skill":{"name":"launcher"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			_, _ = w.Write([]byte(`{"files":[` +
				`{"relpath":"scripts/impeccable","sha256":"sha256:` +
				strings.Repeat("00", 32) +
				`","size":12,"is_binary":false}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	stdout, stderr, status := runCaptured(t, "run", "me/launcher@V1", "--explain", "--json")
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	var answer struct {
		Cwd     string `json:"cwd"`
		CwdMode string `json:"cwd_mode"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answer); err != nil {
		t.Fatalf("--explain --json is not JSON: %v (%q)", err, stdout)
	}
	if answer.CwdMode != "script" || !strings.HasSuffix(answer.Cwd, "/scripts") {
		t.Errorf("cwd = %q (mode %q), want the script's directory under scripts/",
			answer.Cwd, answer.CwdMode)
	}
}

// A requested cache root is checked like the default one (v82 audit): executing from a directory
// other users can write would run content under a public digest's name.
func TestRunRefusesACacheRootOtherUsersCanWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}

	if _, err := runCacheRoot(root); err == nil || !strings.Contains(err.Error(), "不可信") {
		t.Fatalf("err = %v, want a refusal naming the untrusted root", err)
	}
}

// A relative `--cache-in` still produces an absolute skill directory, so `cwd: script` works.
func TestRunResolvesARelativeCacheRootToAbsolute(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	root, err := runCacheRoot("./cache")
	if err != nil {
		t.Fatalf("runCacheRoot: %v", err)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("cache root = %q, want absolute", root)
	}
}

// `--explain -- sh tools/x.sh` answers for that command, not for the contract's default.
func TestRunExplainUsesTheCommandAfterTheSeparator(t *testing.T) {
	contract := canonicalContract(api.RunContract{
		SchemaVersion: 1,
		Files:         []string{"scripts/", "tools/"},
		Cwd:           "script",
		Default:       []string{"sh", "scripts/build.sh"},
		Limits:        api.RunLimits{TimeoutSeconds: 120},
	})
	target := runTarget{base: "me/x", digest: strings.Repeat("a8", 32)}
	delivered := []string{"scripts/build.sh", "tools/deploy.sh"}

	_, note, _, err := explainWorkingDirectory(target, contract, runOptions{}, "/where",
		delivered, []string{"sh", "tools/deploy.sh"}, true)
	if err != nil {
		t.Fatalf("explainWorkingDirectory: %v", err)
	}
	if !strings.Contains(note, "-- 后面的命令") {
		t.Fatalf("note = %q, want the caller's command named", note)
	}
	dir, _, _, _ := explainWorkingDirectory(target, contract, runOptions{}, "/where",
		delivered, []string{"sh", "tools/deploy.sh"}, true)
	if !strings.HasSuffix(dir, filepath.FromSlash("/tools")) {
		t.Fatalf("cwd = %q, want the tools directory", dir)
	}
}

// `path` / `pythonpath` are the declared way to extend the search path; an env override would
// silently replace the value the run builds (v82 audit).
func TestRunRefusesEnvThatShadowsTheSearchPath(t *testing.T) {
	_, err := checkRunContract(api.RunContract{
		SchemaVersion: 1,
		Files:         []string{"scripts/"},
		Env:           map[string]string{"PATH": "/usr/bin"},
		Limits:        api.RunLimits{TimeoutSeconds: 120},
	})
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("err = %v, want the search-path refusal", err)
	}
}

// Two files with the same name are a question the caller can answer by writing a path; saying
// "not found" would send them to install the wrong thing.
func TestRunNamesAnAmbiguousLauncher(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"scripts/tool", "bin/tool"} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	_, err := resolveCommandArgs(dir, []string{"tool"})
	if err == nil || !strings.Contains(err.Error(), "请写清相对路径") {
		t.Fatalf("err = %v, want the ambiguity refusal", err)
	}
}

// `--with` is part of what the run would fetch, so `--explain` counts it and refuses a path the
// manifest does not hold (v82 audit).
func TestRunExplainIncludesWithFilesInTheClosure(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("a9", 32)
	files := map[string]string{
		"scripts/run.sh": "#!/bin/sh\necho hi\n",
		"reference/a.md": "0123456789",
	}
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":{"schemaVersion":1,"files":["scripts/"],` +
				`"path":[],"pythonpath":[],"cwd":"package","env":{},` +
				`"limits":{"timeoutSeconds":120},"default":null},"source":"package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/JD0T518T"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			var rows []string
			for relpath, body := range files {
				rows = append(rows, fmt.Sprintf(
					`{"relpath":%q,"sha256":"sha256:%s","size":%d,"is_binary":false}`,
					relpath, strings.Repeat("00", 32), len(body)))
			}
			_, _ = w.Write([]byte(`{"files":[` + strings.Join(rows, ",") + `]}`))
		default:
			http.NotFound(w, r)
		}
	})

	stdout, stderr, status := runCaptured(t, "run", "me/hello@JD0T518T", "--explain", "--json",
		"--with", "reference/a.md")
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	var answer struct {
		Closure explainClosure `json:"closure"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answer); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, stdout)
	}
	if answer.Closure.Files != 2 {
		t.Fatalf("closure = %+v, want the --with file counted", answer.Closure)
	}

	_, stderr, status = runCaptured(t, "run", "me/hello@JD0T518T", "--explain",
		"--with", "nope.txt")
	if status == 0 || !strings.Contains(stderr, "nope.txt") {
		t.Fatalf("status = %d, stderr = %q; want a refusal naming the path", status, stderr)
	}
}

// `--explain --json` says when the listing cannot settle `cwd: script`: whether a non-whitelisted
// token executes depends on its shebang, which is only known after fetching (v82 audit).
func TestRunExplainMarksAShebangDependentCwd(t *testing.T) {
	withTempHome(t)
	digest := strings.Repeat("b1", 32)
	pointAtServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			_, _ = w.Write([]byte(`{"contract":{"schemaVersion":1,"files":["scripts/"],` +
				`"path":[],"pythonpath":[],"cwd":"script","env":{},` +
				`"limits":{"timeoutSeconds":120},"default":["sh","scripts/tool"]},` +
				`"source":"package","problems":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credentials/lease"):
			_, _ = w.Write([]byte(`{"values":[],"expires_at":"2030-01-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
		case strings.Contains(r.URL.Path, "/author/skills/me/hello/versions/V2"):
			_, _ = w.Write([]byte(`{"skill":{"name":"hello"},"version":{"digest":"sha256:` +
				digest + `"}}`))
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Header().Set("ETag", `"sha256:`+digest+`"`)
			_, _ = w.Write([]byte(`{"files":[{"relpath":"scripts/tool","sha256":"sha256:` +
				strings.Repeat("00", 32) +
				`","size":9,"is_binary":false}]}`))
		default:
			http.NotFound(w, r)
		}
	})

	stdout, stderr, status := runCaptured(t, "run", "me/hello@V2", "--explain", "--json")
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	var answer struct {
		Cwd          string `json:"cwd"`
		CwdUncertain bool   `json:"cwd_uncertain"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answer); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, stdout)
	}
	if !answer.CwdUncertain {
		t.Errorf("cwd_uncertain = false, want true for an extensionless script")
	}
	if !strings.HasSuffix(answer.Cwd, filepath.FromSlash("/scripts")) {
		t.Errorf("cwd = %q, want the script's directory", answer.Cwd)
	}
}

// A malformed `--with` is the caller's flag error, not a network failure (v82 audit).
func TestRunClassifiesAMalformedWithAsUsage(t *testing.T) {
	withTempHome(t)
	_, _, status := runCaptured(t, "run", "me/x", "--with", "../etc/passwd", "--", "true")
	if status != 2 {
		t.Fatalf("status = %d, want 2 (usage)", status)
	}
}
