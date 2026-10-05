package releaseupdate

import "testing"

func TestSelectRelease(t *testing.T) {
	tags := []string{"archied/v1.9.0", "v1.10.0", "archied/v2.0.0-rc.2", "v2.0.0-rc.10", "archie/v9.0.0", "nonsense"}
	for _, tc := range []struct{ channel, pin, want string }{
		{"stable", "", "1.10.0"},
		{"", "", "1.10.0"},
		{"next", "", "2.0.0-rc.10"},
		{"exact-pin", "1.9.0", "1.9.0"},
		{"exact-pin", "2.0.0-rc.2", "2.0.0-rc.2"},
		{"exact-pin", "9.0.0", ""},
		{"exact-pin", "", ""},
		{"invalid", "", ""},
	} {
		t.Run(tc.channel+tc.pin, func(t *testing.T) {
			got, err := SelectRelease(tags, tc.channel, tc.pin)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
