package markdown

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The vectors are shared with the app, which renders the same subset natively.
func TestVectors(t *testing.T) {
	raw, err := os.ReadFile("../../api/vectors/markdown.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []struct {
			Name, Input, HTML string
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range file.Vectors {
		if got := HTML(v.Input); got != v.HTML {
			t.Errorf("%s:\n input %q\n   got %q\n  want %q", v.Name, v.Input, got, v.HTML)
		}
	}
}

func TestOutputNeverContainsInputTags(t *testing.T) {
	for _, in := range []string{
		`<script>alert(1)</script>`,
		`**<img src=x onerror=alert(1)>**`,
		"`<b>`",
		`> <a href="javascript:alert(1)">x</a>`,
		`- "quoted" & 'single'`,
		`*<i>*`,
	} {
		out := HTML(in)
		stripped := out
		for _, tag := range []string{"<p>", "</p>", "<br>", "<blockquote>", "</blockquote>", "<ul>", "</ul>", "<li>", "</li>",
			"<code>", "</code>", "<strong>", "</strong>", "<em>", "</em>"} {
			stripped = strings.ReplaceAll(stripped, tag, "")
		}
		if strings.ContainsAny(stripped, `<>"'`) {
			t.Errorf("%q rendered to %q: raw markup survived", in, out)
		}
	}
}
