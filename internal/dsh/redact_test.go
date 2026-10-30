package dsh

import "testing"

func TestRedactLaunchLine(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		changed bool
	}{
		{
			in:      "dsh web: http://127.0.0.1:3080/?token=abc123",
			want:    "dsh web: http://127.0.0.1:3080/",
			changed: true,
		},
		{
			in:      "dsh web: http://127.0.0.1:3080/?token=abc123 (LAN: http://192.168.1.5:3080/?token=abc123)",
			want:    "dsh web: http://127.0.0.1:3080/",
			changed: true,
		},
		{
			in:      "dsh web: http://127.0.0.1:3080/",
			want:    "dsh web: http://127.0.0.1:3080/",
			changed: false,
		},
		{
			in:      "some unrelated output",
			want:    "some unrelated output",
			changed: false,
		},
	}
	for _, tc := range cases {
		got, changed := RedactLaunchLine(tc.in)
		if got != tc.want || changed != tc.changed {
			t.Errorf("RedactLaunchLine(%q) = (%q, %v), want (%q, %v)", tc.in, got, changed, tc.want, tc.changed)
		}
	}
}
