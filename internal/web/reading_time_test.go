package web

import "testing"

func TestReadingTime(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{"empty", "", ""},
		{"tags only", "<p></p>", ""},
		{"short", "<p>hello world</p>", "1 min read"},
		{"rounds up to one", "one two three four five", "1 min read"},
	}
	for _, c := range cases {
		if got := readingTime(c.html); got != c.want {
			t.Errorf("%s: readingTime(%q) = %q, want %q", c.name, c.html, got, c.want)
		}
	}
}

func TestReadingTimeLongDoc(t *testing.T) {
	words := make([]byte, 0, 4000)
	for i := 0; i < 660; i++ { // 660 words / 220 wpm = 3 min
		words = append(words, []byte("word ")...)
	}
	if got := readingTime("<p>" + string(words) + "</p>"); got != "3 min read" {
		t.Fatalf("got %q, want \"3 min read\"", got)
	}
}
