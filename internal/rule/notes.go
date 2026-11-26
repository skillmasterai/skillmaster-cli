package rule

// VersionNotes says in one line what each rule version changed.
//
// **A notice that asks for a rewrite without saying what changed reads like an injection.** The
// update notice quotes the line for the version it is announcing; when Version moves, add the entry
// in the same commit. The note describes the diff, not the whole text — the agent prints the text
// itself with `self rule print`.
var VersionNotes = map[int]string{
	// v94: the one-line "call the SkillMaster skill" rule became a paragraph.
	2: "把「先调用 SkillMaster skill」换成一段守则：隐私询问放第一位、任何任务先查库、改 skill 先看 help。",
	// v96: the paragraph gained the login line.
	3: "凭据失效时由 Agent 自己运行 skmr login，把链接交给用户点批准，不让用户开终端。",
	// v97: the login line now names the command this release declares.
	4: "登录命令改用正式写法 `skmr auth login`，旧写法 `skmr login` 仍可用。",
}

// VersionNote is the line for a version, empty when this build has none. A release always knows the
// version it carries; an empty note only affects an old working copy.
func VersionNote(version int) string { return VersionNotes[version] }
