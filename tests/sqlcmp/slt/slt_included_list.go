//go:build slt_corpus

package slt

// includedCase tracks one SLT file that should run in TestSLT_InList.
type includedCase struct {
	Label   string // short name shown in logs (e.g. "in1")
	Path    string // relative path inside the corpus root (e.g. "evidence/in1")
	Timeout int    // per-file timeout in seconds
}

// includedCases is the canonical list of SLT test cases that the
// pre-commit gate runs on every change. Keep this list sorted
// alphabetically by Label for easy review.
//
// Edit this slice whenever you add or remove a case from the
// pre-commit gate. The runner picks up changes automatically —
// no codegen needed.
var includedCases = []includedCase{
	{Label: "aggfunc", Path: "evidence/slt_lang_aggfunc", Timeout: 30},
	{Label: "createtrigger", Path: "evidence/slt_lang_createtrigger", Timeout: 30},
	{Label: "createview", Path: "evidence/slt_lang_createview", Timeout: 30},
	{Label: "dropindex", Path: "evidence/slt_lang_dropindex", Timeout: 30},
	{Label: "droptable", Path: "evidence/slt_lang_droptable", Timeout: 30},
	{Label: "droptrigger", Path: "evidence/slt_lang_droptrigger", Timeout: 30},
	{Label: "dropview", Path: "evidence/slt_lang_dropview", Timeout: 30},
	{Label: "idx1000_2", Path: "index/random/1000/slt_good_2", Timeout: 60},
	{Label: "idx1000_3", Path: "index/random/1000/slt_good_3", Timeout: 60},
	{Label: "idx1000_4", Path: "index/random/1000/slt_good_4", Timeout: 60},
	{Label: "in1", Path: "evidence/in1", Timeout: 30},
	{Label: "in2", Path: "evidence/in2", Timeout: 30},
	{Label: "reindex", Path: "evidence/slt_lang_reindex", Timeout: 30},
	{Label: "replace", Path: "evidence/slt_lang_replace", Timeout: 30},
	{Label: "rexp0", Path: "random/expr/slt_good_0", Timeout: 60},
	{Label: "rexp1", Path: "random/expr/slt_good_1", Timeout: 60},
	{Label: "rexp2", Path: "random/expr/slt_good_2", Timeout: 60},
	{Label: "rsel126", Path: "random/select/slt_good_126", Timeout: 30},
	{Label: "select1", Path: "select1", Timeout: 120},
	{Label: "select2", Path: "select2", Timeout: 120},
	{Label: "select3", Path: "select3", Timeout: 600},
	{Label: "select4", Path: "select4", Timeout: 1800},
	{Label: "update", Path: "evidence/slt_lang_update", Timeout: 30},
}
