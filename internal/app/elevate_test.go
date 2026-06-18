package app

import "testing"

func TestParseElevateArg(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"none", []string{"omnigate.exe"}, ""},
		{"space form", []string{"omnigate.exe", "--elevate-update", "kurogames/wutheringwaves"}, "kurogames/wutheringwaves"},
		{"equals form", []string{"omnigate.exe", "--elevate-update=hoyoverse/genshin"}, "hoyoverse/genshin"},
		{"flag without value", []string{"omnigate.exe", "--elevate-update"}, ""},
	}
	for _, c := range cases {
		if got := parseElevateArg(c.args); got != c.want {
			t.Errorf("%s: parseElevateArg=%q want %q", c.name, got, c.want)
		}
	}
}

func TestPendingElevatedGame_returnsOnce(t *testing.T) {
	a := &App{pendingElevate: "kurogames/wutheringwaves"}
	if got := a.PendingElevatedGame(); got != "kurogames/wutheringwaves" {
		t.Fatalf("first call = %q; want the pending game", got)
	}
	if got := a.PendingElevatedGame(); got != "" {
		t.Fatalf("second call = %q; want empty (consumed once)", got)
	}
}
