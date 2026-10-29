package compose

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want Version
		ok   bool
	}{
		{"2.24.4\n", Version{2, 24, 4}, true},
		{"v2.29.1-desktop.1", Version{2, 29, 1}, true},
		{"Docker Compose version v5.6.1", Version{5, 6, 1}, true},
		{"2.24", Version{2, 24, 0}, true},
		{"2.24.4+ds1-0ubuntu1", Version{2, 24, 4}, true},
		{"dev", Version{}, false},
		{"", Version{}, false},
	}
	for _, tt := range tests {
		got, err := ParseVersion(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseVersion(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		v    Version
		want bool
	}{
		{Version{2, 24, 4}, true},
		{Version{2, 24, 3}, false},
		{Version{2, 23, 9}, false},
		{Version{2, 25, 0}, true},
		{Version{3, 0, 0}, true},
		{Version{1, 99, 99}, false},
	}
	for _, tt := range tests {
		if got := tt.v.AtLeast(MinVersion); got != tt.want {
			t.Errorf("%v.AtLeast(%v) = %v", tt.v, MinVersion, got)
		}
	}
}
