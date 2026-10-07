package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEval(t *testing.T) {
	ans := map[string]Answer{"destroys": {Type: "noul", Noul: fp(0.9)}, "remote": {Type: "noul", Noul: fp(0.2)},
		"team": {Type: "choice", Choice: "billing", Confidence: fp(0.7)}, "sev": {Type: "score", Score: fp(1.6)}}
	cases := map[string]bool{"": true, "*": true, "destroys >= 0.8": true, "destroys < 0.8": false,
		"destroys >= 0.8 && remote >= 0.5": false, "destroys >= 0.8 && remote >= 0.5 || sev > 1": true,
		"team == billing": true, "team != billing": false, "team >= 0.6": true, "sev <= 1": false}
	for c, want := range cases {
		if got, err := Eval(c, ans); err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", c, got, err, want)
		}
	}
	if _, err := Eval("nope >= 1", ans); err == nil || !strings.Contains(err.Error(), "not asked") {
		t.Errorf("unknown name must error: %v", err)
	}
	if _, err := Eval("destroys is high", ans); err == nil {
		t.Error("bad syntax must error")
	}
}

func TestOutput(t *testing.T) {
	deny := Decision{Action: "deny", Reason: "r1"}
	warn := Decision{Action: "warn", Reason: "r2"}
	ctx := Decision{Action: "context", Reason: "c"}
	if o := Output("PreToolUse", []Decision{warn, deny}, false); !strings.Contains(o, `"permissionDecision":"deny"`) {
		t.Errorf("deny wins: %s", o)
	}
	if o := Output("PreToolUse", []Decision{warn}, false); !strings.Contains(o, `"permissionDecision":"ask"`) {
		t.Errorf("warn asks: %s", o)
	}
	if o := Output("PreToolUse", []Decision{{Action: "allow"}}, false); o != "" {
		t.Errorf("allow is silent: %s", o)
	}
	if o := Output("Stop", []Decision{{Action: "block", Reason: "b"}}, true); o != "" {
		t.Errorf("stop_hook_active must not block again: %s", o)
	}
	if o := Output("UserPromptSubmit", []Decision{ctx, warn}, false); !strings.Contains(o, `"additionalContext":"c\nWarning: r2"`) {
		t.Errorf("contexts join: %s", o)
	}
	if o := Output("PreToolUse", []Decision{{Action: "exec", Raw: `{"x":1}`}}, false); o != `{"x":1}` {
		t.Errorf("exec output is passed through: %s", o)
	}
}

func TestPluginMatchAndState(t *testing.T) {
	p := Plugin{On: "PreToolUse:Bash|Write"}
	if !p.Matches("PreToolUse", Payload{"tool_name": "Write"}) || p.Matches("PreToolUse", Payload{"tool_name": "Read"}) || p.Matches("Stop", Payload{}) {
		t.Error("tool regex / event matching")
	}
	st, skip := StateOf("PreToolUse", Payload{"tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf x"}, "cwd": "/r"}, "")
	if skip != "" || !strings.Contains(st, "rm -rf x") || !strings.Contains(st, "/r") {
		t.Errorf("PreToolUse state: %q %q", st, skip)
	}
	if got := fill("kind={{kind}} p={{d}}", map[string]Answer{"kind": {Type: "choice", Choice: "ops"}, "d": {Type: "noul", Noul: fp(0.5)}}); got != "kind=ops p=0.50" {
		t.Errorf("fill: %q", got)
	}
}

// Run is the safety path behind bash-guard: answers -> deny / warn / allow, deny before warn, and fail OPEN (allow,
// error recorded) when the endpoint fails, so a broken endpoint never blocks the user's work.
func TestRunBashGuardDecisions(t *testing.T) {
	setHome(t, t.TempDir())
	var probs map[string]float64
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "boom", 500)
			return
		}
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		ans := map[string]any{}
		for k := range req.Questions {
			ans[k] = map[string]any{"type": "noul", "noul": probs[k], "confidence": 0.9}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "stub", "answers": ans})
	}))
	defer srv.Close()
	c := Config{Default: "stub", Profiles: map[string]Profile{"stub": {URL: srv.URL + "/v1/systemone", Model: "stub"}},
		Plugins: map[string]Plugin{}}
	zero := 0
	c.Defaults.Retries = &zero
	guard := Shipped["bash-guard"]
	pay := Payload{"tool_name": "Bash", "tool_input": map[string]any{"command": "rm -rf /srv/data"}}
	for _, tc := range []struct {
		name  string
		probs map[string]float64
		want  string
	}{
		{"destructive and irreversible", map[string]float64{"destroys": 0.91, "irreversible": 0.87, "remote": 0.9}, "deny"},
		{"destructive but recoverable", map[string]float64{"destroys": 0.6, "irreversible": 0.2, "remote": 0.1}, "warn"},
		{"harmless", map[string]float64{"destroys": 0.05, "irreversible": 0.05, "remote": 0.05}, "allow"},
	} {
		probs = tc.probs
		d, rec := c.Run("bash-guard", guard, "PreToolUse", pay)
		if d.Action != tc.want || rec["error"] != nil {
			t.Errorf("%s: got %q (err %v), want %q", tc.name, d.Action, rec["error"], tc.want)
		}
		if tc.want == "deny" && !strings.Contains(d.Reason, "destroys") {
			t.Errorf("a deny says why: %q", d.Reason)
		}
	}
	fail = true
	d, rec := c.Run("bash-guard", guard, "PreToolUse", pay)
	if d.Action != "allow" || rec["error"] == nil {
		t.Fatalf("endpoint failure must fail open with the error recorded: %q %v", d.Action, rec["error"])
	}
}

// An --exec plugin hands the event to a command: its stdout is the decision, it gets the event JSON on stdin, and a
// failing or hanging command fails OPEN (allow, error recorded) instead of blocking every tool call.
func TestRunExecPlugin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	setHome(t, t.TempDir())
	c := Config{Plugins: map[string]Plugin{}}
	pay := Payload{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}}
	pl := Plugin{On: "PreToolUse:Bash", Exec: `grep -q '"command":"ls"' && echo '{"decision":"x"}'`}
	if d, rec := c.Run("ext", pl, "PreToolUse", pay); d.Action != "exec" || d.Raw != `{"decision":"x"}` || rec["error"] != nil {
		t.Fatalf("stdout passes through and stdin carries the event: %+v %v", d, rec["error"])
	}
	pl.Exec = "exit 3"
	if d, rec := c.Run("ext", pl, "PreToolUse", pay); d.Action != "allow" || rec["error"] == nil {
		t.Fatalf("a failing command fails open: %+v", d)
	}
	old := ExecTimeout
	ExecTimeout = 300 * time.Millisecond
	defer func() { ExecTimeout = old }()
	pl.Exec = "sleep 5"
	t0 := time.Now()
	d, rec := c.Run("ext", pl, "PreToolUse", pay)
	if d.Action != "allow" || !strings.Contains(fmt.Sprint(rec["error"]), "no answer within") || time.Since(t0) > 3*time.Second {
		t.Fatalf("a hung command fails open within the timeout: %+v %v after %s", d, rec["error"], time.Since(t0))
	}
}
