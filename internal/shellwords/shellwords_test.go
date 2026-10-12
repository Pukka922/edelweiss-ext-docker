package shellwords

import (
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string][]string{
		"":                           nil,
		"   ":                        nil,
		"nginx -g 'daemon off;'":     {"nginx", "-g", "daemon off;"},
		`sh -c "echo \"hi\" \$HOME"`: {"sh", "-c", `echo "hi" $HOME`},
		`a\ b c`:                     {"a b", "c"},
		`'it''s'`:                    {"its"},
		"x\t y\n z":                  {"x", "y", "z"},
		`"" ''`:                      {"", ""},
		`--opt="a b"`:                {"--opt=a b"},
	}
	for in, want := range cases {
		got, err := Split(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Split(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`'open`, `"open`, `trailing\`} {
		if _, err := Split(bad); err == nil {
			t.Errorf("Split(%q): want an error", bad)
		}
	}
}

func TestJoinRoundTrip(t *testing.T) {
	for _, words := range [][]string{
		{"nginx", "-g", "daemon off;"},
		{"sh", "-c", `echo "it's" $HOME`},
		{""},
		{"plain", "--flag=value", "/usr/bin/x"},
	} {
		joined := Join(words)
		back, err := Split(joined)
		if err != nil || !slices.Equal(back, words) {
			t.Errorf("Join(%q) = %q, splits back to %q, %v", words, joined, back, err)
		}
	}
	if got := Join([]string{"nginx", "-g", "daemon off;"}); got != "nginx -g 'daemon off;'" {
		t.Errorf("Join = %q", got)
	}
}

// Join quotes a word only if a shell would split or expand it: harmless characters (= % ! { } and # or ~ inside a word)
// stay as they are, so that a command line in the form reads like the one a person typed.
func TestJoinQuotesOnlyWhatNeedsIt(t *testing.T) {
	cases := []struct {
		words []string
		want  string
	}{
		{[]string{"node", "--max-old-space-size=4096", "app.js"}, "node --max-old-space-size=4096 app.js"},
		{[]string{"nginx", "-g", "daemon off;"}, "nginx -g 'daemon off;'"},
		{[]string{"env", "A=b", "PCT=100%", "{x}", "hi!"}, "env A=b PCT=100% {x} hi!"},
		{[]string{"#x"}, `'#x'`},
		{[]string{"a#b"}, "a#b"},
		{[]string{"~"}, `'~'`},
		{[]string{"~/bin"}, `'~/bin'`},
		{[]string{"a~b"}, "a~b"},
		{[]string{""}, "''"},
		{[]string{"a", "", "b"}, "a '' b"},
		{[]string{"two words"}, `'two words'`},
		{[]string{"tab\there", "line\nbreak"}, "'tab\there' 'line\nbreak'"},
		{[]string{"it's"}, `'it'\''s'`},
		{[]string{`say "hi"`}, `'say "hi"'`},
		{[]string{`back\slash`}, `'back\slash'`},
		{[]string{"$HOME", "`id`"}, "'$HOME' '`id`'"},
		{[]string{"a;b", "a&b", "a|b", "a<b", "a>b"}, "'a;b' 'a&b' 'a|b' 'a<b' 'a>b'"},
		{[]string{"(x)", "*.go", "a?", "[a]"}, "'(x)' '*.go' 'a?' '[a]'"},
		{[]string{"nbsp here"}, "'nbsp here'"},
	}
	for _, c := range cases {
		got := Join(c.words)
		if got != c.want {
			t.Errorf("Join(%q) = %q, want %q", c.words, got, c.want)
		}
		if back, err := Split(got); err != nil || !slices.Equal(back, c.words) {
			t.Errorf("Join(%q) = %q splits back to %q, %v", c.words, got, back, err)
		}
	}
}
