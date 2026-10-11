package youtube

import "testing"

func TestParseVideoID(t *testing.T) {
	ok := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":          "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?t=10":                    "dQw4w9WgXcQ",
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ&list=x": "dQw4w9WgXcQ",
		"https://m.youtube.com/shorts/dQw4w9WgXcQ":             "dQw4w9WgXcQ",
		"youtube.com/embed/dQw4w9WgXcQ":                        "dQw4w9WgXcQ",
	}
	for in, want := range ok {
		got, err := ParseVideoID(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	bad := []string{"", "https://evil.com/watch?v=dQw4w9WgXcQ", "https://youtube.com.evil.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=short", "javascript:alert(1)", "https://www.youtube.com/"}
	for _, in := range bad {
		if _, err := ParseVideoID(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}
