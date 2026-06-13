package fleet

import "testing"

func TestSanitizeLabel(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Living Room", "living-room"},
		{"AV / Media", "av-media"},
		{"Kitchen", "kitchen"},
		{"Front Office", "front-office"},
		{"  leading-trailing  ", "leading-trailing"},
		{"---leading-dashes", "leading-dashes"},
		{"trailing-dashes---", "trailing-dashes"},
		{"UPPER CASE", "upper-case"},
		{"multi   spaces", "multi-spaces"},
		{"slash/slash", "slash-slash"},
		{"dot.ok_under", "dot.ok_under"},
		{"", ""},
		// 64 chars in -> truncated to 63.
		{"abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz00", "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0"},
		// starts/ends with non-alnum after lower
		{"_leading", "leading"},
		{"trailing_", "trailing"},
		// only invalid chars
		{"---", ""},
		{"/ /", ""},
	}
	for _, tc := range tests {
		got := SanitizeLabel(tc.in)
		if got != tc.want {
			t.Errorf("SanitizeLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
