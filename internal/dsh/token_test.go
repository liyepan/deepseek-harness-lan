package dsh

import "testing"

func TestTokenFromLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "plain startup line",
			line: "dsh web: http://127.0.0.1:3080/?token=RdkZFTlSkonGWnWGokuWeYI9453VK9VbwirgbqE0PAU",
			want: "RdkZFTlSkonGWnWGokuWeYI9453VK9VbwirgbqE0PAU",
		},
		{
			name: "with lan suffix",
			line: "dsh web: http://127.0.0.1:3080/?token=abc123 (LAN: http://192.168.1.5:3080/?token=abc123)",
			want: "abc123",
		},
		{
			name: "unrelated line",
			line: "dsh web: opening the default browser",
			want: "",
		},
		{
			name: "no token",
			line: "dsh web: http://127.0.0.1:3080/",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TokenFromLine(tc.line); got != tc.want {
				t.Fatalf("TokenFromLine(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestArgsMatchTheDocumentedCommand(t *testing.T) {
	got := Args("127.0.0.1", 3080)
	want := []string{"web", "--host", "127.0.0.1", "--port", "3080", "--no-open"}
	if len(got) != len(want) {
		t.Fatalf("Args() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Args()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
