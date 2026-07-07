package solve

import "testing"

func TestDeriveAsserts(t *testing.T) {
	cases := []struct {
		prompt string
		want   []string
	}{
		{
			"Write is_palindrome(s). Examples: is_palindrome('Race car') is True, is_palindrome('hello') is False.",
			[]string{"assert is_palindrome('Race car') == True", "assert is_palindrome('hello') == False"},
		},
		{
			"parse_duration('2h30m') == 9000 and parse_duration('45s') returns 45",
			[]string{"assert parse_duration('2h30m') == 9000", "assert parse_duration('45s') == 45"},
		},
		{
			"Write a Python function reverse_words(sentence) that reverses word order. Example: 'hello world foo' becomes 'foo world hello'.",
			[]string{"assert reverse_words('hello world foo') == 'foo world hello'"},
		},
		{
			"Explain how photosynthesis works.",
			nil,
		},
	}
	for _, c := range cases {
		got := DeriveAsserts(c.prompt)
		if len(got) != len(c.want) {
			t.Errorf("DeriveAsserts(%.40q) = %v, want %v", c.prompt, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("DeriveAsserts(%.40q)[%d] = %q, want %q", c.prompt, i, got[i], c.want[i])
			}
		}
	}
}
