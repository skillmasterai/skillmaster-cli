// How `run` turns a version's contract into a command line, a working directory and an environment.
//
// **The contract is the authority for all three.** Command resolution asks which files the version
// actually delivered; the working directory is one of the contract's three modes; and PATH /
// PYTHONPATH are the contract's declarations, resolved inside the version root. Nothing here reads
// the caller's machine except `PATH` (to keep a system command a system command) and the caller's
// own working directory (`project` mode, and a relative `--cwd`).

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skillmasterai/skillmaster-cli/internal/api"
	"github.com/skillmasterai/skillmaster-cli/internal/config"
)

// scriptExtensions are the arguments `run` will try to resolve inside the closure.
//
// **A whitelist rather than "anything that is not a flag".** The arguments a skill's commands take
// are full of paths that are not scripts — an input CSV, an output directory, a URL — and rewriting
// one of those would hand the script a path it did not ask for. Only the shapes that are actually
// executed are touched (ADR 0222 §决定 4).
var scriptExtensions = map[string]bool{
	".py": true, ".sh": true, ".bash": true, ".js": true, ".mjs": true,
	".cjs": true, ".ts": true, ".rb": true, ".pl": true, ".ps1": true,
}

// resolveCommandArgs turns the script paths in a command into absolute ones.
//
// **The closure, not a fixed directory.** Since ADR 0222 the version declares which files it
// delivers, and what `run` can resolve is exactly what it delivered: a token naming a real file is
// rewritten only when its extension is executable or its first line is `#!`, and everything else —
// data files, flags, URLs — is left exactly as the author wrote it.
//
// **The first word participates, with one guard.** An extensionless launcher (`impeccable`) could
// not be executed before; it can now, but a bare word that `PATH` resolves stays with `PATH`, so a
// package cannot shadow `git` or `ls` by shipping a file with that name. A word that is spelled with
// a separator (`./impeccable`) is a path the caller already made explicit, and is resolved here
// regardless of `PATH`.
func resolveCommandArgs(skillDir string, command []string) ([]string, error) {
	out := make([]string, len(command))
	copy(out, command)
	if len(out) == 0 {
		return out, nil
	}
	resolved, rewritten, err := resolveFirstWord(skillDir, out[0])
	if err != nil {
		return nil, err
	}
	if rewritten {
		out[0] = resolved
	}
	for index := 1; index < len(out); index++ {
		arg := out[index]
		if filepath.IsAbs(arg) || strings.HasPrefix(arg, "-") || strings.Contains(arg, "://") {
			continue
		}
		scriptLike := scriptExtensions[strings.ToLower(filepath.Ext(arg))]
		spelled := looksSpelled(arg)
		if !scriptLike && !spelled {
			// A bare word of unknown shape can still be an extensionless launcher named without
			// `./`. It is rewritten only when it is a real file whose first line is `#!`; a data
			// value that merely happens to share a name with a file stays untouched.
			if resolved, err := lookupClosureFile(skillDir, arg); err == nil && hasShebang(resolved) {
				out[index] = resolved
			}
			continue
		}
		resolved, err := lookupClosureFile(skillDir, arg)
		if err != nil {
			if scriptLike {
				// `nope.py` is a script the author meant to run; not finding it is a refusal, with
				// what the closure does contain. This is the pre-v82 behaviour, kept.
				return nil, err
			}
			// A spelled data path (`./out/report.csv`) that is not in the closure is the caller's
			// own path, not a script. Leave it alone (A3: data arguments are unchanged).
			continue
		}
		if scriptLike || hasShebang(resolved) {
			out[index] = resolved
		}
	}
	return out, nil
}

// resolveRunCommand is `resolveCommandArgs` plus the script it resolved.
//
// The script is the first token the resolver rewrote to an absolute path. It is what `cwd: script`
// starts the child in — the directory of the file the command actually names, so `python plan.py`
// and `python scripts/plan.py` land in the same place.
func resolveRunCommand(skillDir string, command []string) ([]string, string, error) {
	resolved, err := resolveCommandArgs(skillDir, command)
	if err != nil {
		return nil, "", err
	}
	script := ""
	for index := range resolved {
		if resolved[index] != command[index] && filepath.IsAbs(resolved[index]) {
			script = resolved[index]
			break
		}
	}
	return resolved, script, nil
}

// resolveFirstWord rewrites a launcher, and says whether it did.
//
// **`PATH` wins for a bare word.** The command `git status` means git even when the skill ships a
// file named `git`; only a name `PATH` cannot find is looked up in the closure. A token with a
// separator is a path the caller wrote, so the closure answers it directly.
func resolveFirstWord(skillDir, word string) (string, bool, error) {
	if filepath.IsAbs(word) || strings.HasPrefix(word, "-") || strings.Contains(word, "://") {
		return "", false, nil
	}
	spelled := looksSpelled(word)
	if !spelled {
		if _, err := exec.LookPath(word); err == nil {
			return "", false, nil
		}
	}
	resolved, err := lookupClosureFile(skillDir, word)
	if err != nil {
		var ambiguous ambiguousClosure
		if errors.As(err, &ambiguous) {
			// Two files share the name: saying "not found" would send the caller looking for the
			// missing interpreter instead of writing the path (v82 audit).
			return "", false, err
		}
		if spelled {
			// `./impeccable` promises a file; not finding it is the caller's mistake, not a
			// missing interpreter.
			return "", false, err
		}
		// An ordinary command this machine lacks is reported by exec when it runs, with the
		// sentence that tells the person how to install it.
		return "", false, nil
	}
	if !hasShebang(resolved) {
		// The first word is executed; a file without `#!` is not a launcher. Rewriting it would
		// turn "permission denied" into a path the shell could try and still fail on.
		return "", false, nil
	}
	return resolved, true, nil
}

// looksSpelled says whether a token was written as a path (`./x`, `bin/x`, `..\x`).
func looksSpelled(token string) bool {
	return strings.ContainsRune(token, '/') || strings.ContainsRune(token, '\\')
}

// lookupClosureFile finds a token in the materialized closure.
//
// **Only `scripts/`'s old search rules are kept.** An exact relative path wins; otherwise a unique
// basename anywhere in the closure is accepted; two matches are refused rather than guessed (running
// the wrong file is worse than stopping). A `..` is never a name to look up.
func lookupClosureFile(root, arg string) (string, error) {
	cleaned := path.Clean(filepath.ToSlash(arg))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", notInClosure(root, arg)
	}
	direct := filepath.Join(root, filepath.FromSlash(cleaned))
	if isFile(direct) && inside(root, direct) {
		return direct, nil
	}
	matches := findBaseName(root, filepath.Base(cleaned))
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", notInClosure(root, arg)
	default:
		return "", ambiguousClosure{fmt.Sprintf("这一版的运行闭包里有 %d 个 %s：%s；请写清相对路径",
			len(matches), filepath.Base(cleaned), strings.Join(relativeTo(root, matches), "、"))}
	}
}

// ambiguousClosure is "the name exists more than once", as distinct from "the name exists
// nowhere": the first is answered by writing a path, the second by installing something.
type ambiguousClosure struct{ message string }

func (e ambiguousClosure) Error() string { return e.message }

func notInClosure(root, arg string) error {
	return fmt.Errorf("这一版的运行闭包里没有 %s，现有：%s", arg, listClosure(root))
}

// hasShebang reports whether a file starts with `#!`. Only the first two bytes are read: a script
// may be larger than a run should hold, and the question is answered by the first line.
func hasShebang(file string) bool {
	handle, err := os.Open(file)
	if err != nil {
		return false
	}
	defer handle.Close()
	head := make([]byte, 2)
	read, _ := handle.Read(head)
	return read == 2 && head[0] == '#' && head[1] == '!'
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// inside says whether path is a descendant of dir, after both are made absolute.
func inside(dir, path string) bool {
	relative, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func findBaseName(dir, name string) []string {
	var found []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.Name() == runMarker {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			found = append(found, findBaseName(path, name)...)
			continue
		}
		if entry.Name() == name {
			found = append(found, path)
		}
	}
	return found
}

// listClosure names what the version delivered, for the refusal that says a name was not there.
func listClosure(root string) string {
	var names []string
	_ = filepath.Walk(root, func(current string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == runMarker && filepath.Dir(current) == root {
			return nil
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr == nil {
			names = append(names, filepath.ToSlash(relative))
		}
		return nil
	})
	if len(names) == 0 {
		return "（没有文件）"
	}
	sort.Strings(names)
	return strings.Join(names, "、")
}

func relativeTo(base string, paths []string) []string {
	out := make([]string, len(paths))
	for index, path := range paths {
		if rel, err := filepath.Rel(base, path); err == nil {
			out[index] = filepath.ToSlash(rel)
			continue
		}
		out[index] = path
	}
	return out
}

// workingDirectory answers the contract's three cwd modes, with `--cwd` on top.
//
// **`script` is the directory of the script this command actually named**, so `python plan.py` and
// `python scripts/plan.py` agree; with nothing resolved it falls back to the version root.
// `package` is the version root and `project` is the caller's own directory — the mode that lets a
// scanner look at the project instead of the cache.
//
// **A relative `--cwd` is the caller's path**, not the version's: a person typing `--cwd sub` means
// `./sub` where they stand. The directory must exist; starting a child in a directory that was
// silently created would hide a typo.
func workingDirectory(skillDir, mode, script, requested, callerCwd string) (string, error) {
	if requested != "" {
		dir := requested
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(callerCwd, dir)
		}
		dir = filepath.Clean(dir)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("--cwd 指向的目录不存在：%s", requested)
		}
		return dir, nil
	}
	switch mode {
	case "package":
		return skillDir, nil
	case "project":
		return callerCwd, nil
	case "", "script":
		if script != "" {
			return filepath.Dir(script), nil
		}
		return skillDir, nil
	default:
		return "", fmt.Errorf("运行契约的 cwd 只能是 script / package / project，实际是 %q", mode)
	}
}

// inferredContract is what a version without any declaration runs under (ADR 0222 §决定 2).
//
// **The pre-v82 default, made explicit**: the closure is `scripts/` and the working directory is
// the resolved script's. A skill that ran before keeps running; `--explain` says `inferred`.
func inferredContract() api.RunContract {
	return canonicalContract(api.RunContract{
		SchemaVersion: 1,
		Files:         []string{"scripts/"},
		Cwd:           "script",
		Limits:        api.RunLimits{TimeoutSeconds: 120},
	})
}

// canonicalContract is the one spelling of a contract the hash and the cache key are taken over.
//
// **Absent and empty are the same declaration.** The server writes `[]` / `{}`; a contract this
// client defaults has nil fields. Without this, the inferred default and the server's identical
// default would hash differently and every offline fallback would miss the directory the online
// run wrote.
func canonicalContract(contract api.RunContract) api.RunContract {
	if len(contract.Files) == 0 {
		contract.Files = []string{"scripts/"}
	}
	if contract.Path == nil {
		contract.Path = []string{}
	}
	if contract.Pythonpath == nil {
		contract.Pythonpath = []string{}
	}
	if contract.Env == nil {
		contract.Env = map[string]string{}
	}
	if len(contract.Default) == 0 {
		// The TD's default for `default` is null; an empty array means the same thing.
		contract.Default = nil
	}
	return contract
}

// checkRunContract validates a contract the server handed back.
//
// **Fail closed on a contract this client cannot honour** (v82 Q5). The server validates at publish
// time, but a version may predate that validation, and a newer `schemaVersion` describes fields
// this client would silently ignore — misreading it is worse than stopping. Unknown *fields* are
// ignored (a typed struct drops them), which is the forward-compatible half of the same rule.
func checkRunContract(contract api.RunContract) (api.RunContract, error) {
	contract = canonicalContract(contract)
	if contract.SchemaVersion == 0 {
		contract.SchemaVersion = 1
	}
	if contract.SchemaVersion != 1 {
		return contract, fmt.Errorf("运行契约的 schemaVersion 是 %d，这个版本的 skmr 只认识 1：请升级 skmr",
			contract.SchemaVersion)
	}
	for _, entry := range contract.Files {
		if err := validateContractPath("files", entry); err != nil {
			return contract, err
		}
	}
	for _, entry := range contract.Path {
		if err := validateContractPath("path", entry); err != nil {
			return contract, err
		}
	}
	for _, entry := range contract.Pythonpath {
		if err := validateContractPath("pythonpath", entry); err != nil {
			return contract, err
		}
	}
	for _, entry := range contract.Default {
		if err := validateContractPath("default", entry); err != nil {
			return contract, err
		}
	}
	switch contract.Cwd {
	case "":
		contract.Cwd = "script"
	case "script", "package", "project":
	default:
		return contract, fmt.Errorf("运行契约的 cwd 只能是 script / package / project，实际是 %q",
			contract.Cwd)
	}
	if contract.Limits.TimeoutSeconds <= 0 {
		contract.Limits.TimeoutSeconds = 120
	}
	if contract.Limits.TimeoutSeconds > 1800 {
		return contract, fmt.Errorf("运行契约的 limits.timeoutSeconds 是 %d，上限是 1800",
			contract.Limits.TimeoutSeconds)
	}
	for key := range contract.Env {
		// Case-insensitive: Windows environment names are, and `exec` keeps the last duplicate
		// (v82 audit).
		if strings.HasPrefix(strings.ToUpper(key), "SKILLMASTER_") {
			return contract, fmt.Errorf("运行契约的 env 不能覆盖保留变量 %s", key)
		}
		// `path` / `pythonpath` are the declared way to extend these; an env entry with the same
		// name would replace the value the run built from the closure (v82 audit).
		switch strings.ToUpper(key) {
		case "PATH", "PYTHONPATH":
			return contract, fmt.Errorf(
				"运行契约的 env 不能设置 %s；搜索路径用 path / pythonpath 声明", key)
		}
	}
	return contract, nil
}

func validateContractPath(field, value string) error {
	if value == "" {
		return fmt.Errorf("运行契约的 %s 里有空路径", field)
	}
	cleaned := path.Clean(filepath.ToSlash(value))
	if path.IsAbs(cleaned) || filepath.IsAbs(value) || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("运行契约的 %s 只接受版本内的相对路径，%q 不是", field, value)
	}
	return nil
}

// contractClosure filters the manifest to the files the contract delivers.
//
// A `files` entry ending in `/` is a directory prefix; anything else is an exact path. The order is
// the manifest's, so two runs fetch in the same order.
func contractClosure(files []api.File, entries []string) []api.File {
	var closure []api.File
	for _, file := range files {
		if contractDelivers(entries, file.Relpath) {
			closure = append(closure, file)
		}
	}
	return closure
}

func contractDelivers(entries []string, relpath string) bool {
	for _, entry := range entries {
		if strings.HasSuffix(entry, "/") {
			if strings.HasPrefix(relpath, entry) {
				return true
			}
			continue
		}
		if relpath == entry {
			return true
		}
	}
	return false
}

// closureStats is `--explain`'s closure line: how many files, how many bytes.
func closureStats(closure []api.File) (int, int64) {
	total := int64(0)
	for _, file := range closure {
		total += file.Size
	}
	return len(closure), total
}

// contractDirectories resolves a contract's `path` / `pythonpath` entries inside the version root.
func contractDirectories(skillDir, field string, values []string) ([]string, error) {
	dirs := make([]string, 0, len(values))
	for _, value := range values {
		if err := validateContractPath(field, value); err != nil {
			return nil, err
		}
		dirs = append(dirs, filepath.Join(skillDir, filepath.FromSlash(path.Clean(value))))
	}
	return dirs, nil
}

// contractEnvironment is what the contract adds to the child's environment.
//
// **PATH only ever grows at the front**, so a declared tool directory can win while the machine's
// own commands remain reachable. PYTHONPATH follows the same rule. The reserved `SKILLMASTER_`
// namespace is not writable from a contract (checked in `checkRunContract`), which is what keeps
// `SKILLMASTER_SKILL_DIR` and the credential variables the CLI's to set.
func contractEnvironment(contract api.RunContract, skillDir string) ([]string, error) {
	var extra []string
	if dirs, err := contractDirectories(skillDir, "path", contract.Path); err != nil {
		return nil, err
	} else if len(dirs) > 0 {
		value := strings.Join(dirs, string(os.PathListSeparator))
		if existing := os.Getenv("PATH"); existing != "" {
			value += string(os.PathListSeparator) + existing
		}
		extra = append(extra, "PATH="+value)
	}
	if dirs, err := contractDirectories(skillDir, "pythonpath", contract.Pythonpath); err != nil {
		return nil, err
	} else if len(dirs) > 0 {
		value := strings.Join(dirs, string(os.PathListSeparator))
		if existing := os.Getenv("PYTHONPATH"); existing != "" {
			value += string(os.PathListSeparator) + existing
		}
		extra = append(extra, "PYTHONPATH="+value)
	}
	for key, value := range contract.Env {
		extra = append(extra, key+"="+value)
	}
	return extra, nil
}

// validateWithPath is the shape check `--with` applies before asking the server.
//
// **The manifest is the only source of paths.** A `--with` naming something the version does not
// contain is not a request to fetch by name — it is a path outside the package, and the answer is
// no (ADR 0222 §决定 7). Absolute paths and anything climbing with `..` are refused here; whether
// the path exists is decided against the manifest.
func validateWithPath(value string) error {
	if value == "" {
		return errors.New("--with 后面要跟一个版本内的相对路径")
	}
	if strings.ContainsRune(value, '\\') {
		return fmt.Errorf("--with 的路径里不能有反斜杠：%s", value)
	}
	cleaned := path.Clean(value)
	if path.IsAbs(cleaned) || filepath.IsAbs(value) || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("--with 只接受版本内的相对路径：%s", value)
	}
	return nil
}

// cacheKey is the short digest used in state paths and headers.
func cacheKey(digest string) string {
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

// cacheRuleVersion is the rule the directories under the cache were written with.
//
// **A digest names the file bytes, not their permissions or their scope** (ADR 0166). v82 changes
// both the fetch scope (the contract's closure, ADR 0222) and the directory name (the contract
// hash joins it, because a run profile can change without changing the digest). Directories written
// under an older rule are a miss, not a hit; nothing removes a cache (ADR 0101).
const cacheRuleVersion = 4

func cacheDirName(digest, contractHash string) string {
	return fmt.Sprintf("%s-r%d-c%s", cacheKey(digest), cacheRuleVersion, contractHash)
}

// contractHash is the contract's identity for the cache directory name.
//
// **Canonical bytes.** The same contract is hashed from two directions — fresh from the server and
// read back from a run marker — so the hash is taken over the typed struct's own marshalling, whose
// field order is fixed and whose map keys are sorted. A profile edit changes this hash and therefore
// the directory, even though the version digest is unchanged (ADR 0222 后果).
func contractHash(contract api.RunContract) string {
	// `json.Marshal` on a struct of strings, slices and maps cannot fail.
	data, _ := json.Marshal(canonicalContract(contract))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:8]
}

// predictedSkillDir is where this contract's closure would land, computed without creating anything.
//
// `--explain` must not materialize, but a person asking where a run starts deserves the real path;
// the only case this cannot name is `--no-cache`, whose directory is made at run time.
func predictedSkillDir(cacheIn, digest string, contract api.RunContract) string {
	root := cacheIn
	if root == "" {
		root = filepath.Join(os.TempDir(), config.Name, "cache")
	} else if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return filepath.Join(root, cacheDirName(digest, contractHash(contract)))
}

// runCacheRoot is where digest-addressed directories live.
//
// **Checked, not trusted.** `$TMPDIR` is `/tmp` on Linux and the directory is keyed by a public
// digest; a root another user created would be content this run executes under a name it did not
// earn. A root that fails the check is replaced by a private temporary directory for this run.
func runCacheRoot(requested string) (string, error) {
	root := requested
	if root == "" {
		root = filepath.Join(os.TempDir(), config.Name, "cache")
	} else {
		// **Absolute, always.** A relative root would make the materialized directory relative
		// too; the command's script path would then not be absolute, `cwd: script` would fall
		// back to the version root, and the child would resolve the argument from there.
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("解析缓存目录：%w", err)
		}
		root = absolute
	}
	if err := privateCacheRoot(root); err != nil {
		if requested == "" {
			parent, mkErr := os.MkdirTemp("", config.Name+"-cache-")
			if mkErr != nil {
				return "", fmt.Errorf("创建临时目录：%w", mkErr)
			}
			fmt.Fprintf(os.Stderr, "缓存目录 %s 不可信（%v），这次改用 %s。\n", root, err, parent)
			return parent, nil
		}
		// A requested root is the caller's choice, but executing content from a directory other
		// users can write is not: the marker and digest are public, so a planted directory would
		// run somebody else's files under this version's name (v82 audit).
		return "", fmt.Errorf("缓存目录 %s 不可信（%v）：它得是你自己的、其他用户不可写的；"+
			"换一个目录，或 chmod 0700 %s", root, err, root)
	}
	return root, nil
}
