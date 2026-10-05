package app

import (
	"reflect"
	"testing"
)

func TestParseElevateBundleArg(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want ElevatedBundle
	}{
		{"valid", []string{"x", "--elevate-install-bundle", "kurogames/wutheringwaves", "SD"}, ElevatedBundle{GameID: "kurogames/wutheringwaves", Bundle: "SD"}},
		{"unknown bundle", []string{"x", "--elevate-install-bundle", "kurogames/wutheringwaves", "4K"}, ElevatedBundle{}},
		{"missing bundle", []string{"x", "--elevate-install-bundle", "kurogames/wutheringwaves"}, ElevatedBundle{}},
		{"missing both", []string{"x", "--elevate-install-bundle"}, ElevatedBundle{}},
		{"update flag only", []string{"x", "--elevate-update", "kurogames/wutheringwaves"}, ElevatedBundle{}},
		{"none", []string{"x"}, ElevatedBundle{}},
	}
	for _, c := range cases {
		if got := parseElevateBundleArg(c.args); got != c.want {
			t.Errorf("%s: parseElevateBundleArg=%+v want %+v", c.name, got, c.want)
		}
	}
}

func TestPendingElevatedBundle_returnsOnce(t *testing.T) {
	want := ElevatedBundle{GameID: "kurogames/wutheringwaves", Bundle: "SD"}
	a := &App{pendingElevateBundle: want}
	if got := a.PendingElevatedBundle(); got != want {
		t.Fatalf("first call = %+v; want %+v", got, want)
	}
	if got := a.PendingElevatedBundle(); got != (ElevatedBundle{}) {
		t.Fatalf("second call = %+v; want zero (consumed once)", got)
	}
}

// stubElevation replaces the elevation seams; relaunch args are captured into
// *calls. Seams are restored via t.Cleanup.
func stubElevation(t *testing.T, elevated bool) *[][]string {
	t.Helper()
	origIs, origRelaunch := isElevatedFn, relaunchElevatedFn
	t.Cleanup(func() { isElevatedFn, relaunchElevatedFn = origIs, origRelaunch })
	calls := &[][]string{}
	isElevatedFn = func() bool { return elevated }
	relaunchElevatedFn = func(args []string) error {
		*calls = append(*calls, append([]string(nil), args...))
		return nil
	}
	return calls
}

func TestRelaunchElevatedForBundle_args(t *testing.T) {
	a, _ := newBundleApp(t)
	calls := stubElevation(t, false)
	if err := a.RelaunchElevatedForBundle("kurogames/wutheringwaves", "SD"); err != nil {
		t.Fatalf("err = %v", err)
	}
	want := [][]string{{"--elevate-install-bundle", "kurogames/wutheringwaves", "SD"}}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("relaunch args = %v; want %v", *calls, want)
	}
}

func TestRelaunchElevatedForBundle_unknownBundle(t *testing.T) {
	a, _ := newBundleApp(t)
	calls := stubElevation(t, false)
	if err := a.RelaunchElevatedForBundle("kurogames/wutheringwaves", "4K"); err == nil {
		t.Fatal("want error for unknown bundle")
	}
	if len(*calls) != 0 {
		t.Fatalf("relaunch called: %v", *calls)
	}
}

func TestRelaunchElevatedForBundle_alreadyElevated(t *testing.T) {
	a, _ := newBundleApp(t)
	calls := stubElevation(t, true)
	err := a.RelaunchElevatedForBundle("kurogames/wutheringwaves", "SD")
	if err == nil || err.Error() != "already_elevated" {
		t.Fatalf("err = %v; want already_elevated", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("relaunch called: %v", *calls)
	}
}

func TestRelaunchElevatedForBundle_badGameID(t *testing.T) {
	for _, g := range []string{
		"x --elevate-update kurogames/wutheringwaves",
		"-x",
		"hoyoverse/genshin", // no BundleManager
		`kurogames/"wutheringwaves`,
		"",
	} {
		a, _ := newBundleApp(t)
		calls := stubElevation(t, false)
		if err := a.RelaunchElevatedForBundle(g, "SD"); err == nil {
			t.Errorf("%q: want error", g)
		}
		if len(*calls) != 0 {
			t.Errorf("%q: relaunch called: %v", g, *calls)
		}
	}
}

func TestRelaunchElevated_usesSeam(t *testing.T) {
	a := &App{}
	calls := stubElevation(t, false)
	if err := a.RelaunchElevated("kurogames/wutheringwaves"); err != nil {
		t.Fatalf("err = %v", err)
	}
	want := [][]string{{"--elevate-update", "kurogames/wutheringwaves"}}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("relaunch args = %v; want %v", *calls, want)
	}
}

func TestRelaunchElevatedForBundle_uacDeclined(t *testing.T) {
	a, _ := newBundleApp(t)
	stubElevation(t, false)
	relaunchElevatedFn = func([]string) error { return errUACDeclined }
	if err := a.RelaunchElevatedForBundle("kurogames/wutheringwaves", "SD"); err != errUACDeclined {
		t.Fatalf("err = %v; want errUACDeclined", err)
	}
}

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

// RelaunchAsAdmin relaunches with no auto-start argument (bundle removal).
func TestRelaunchAsAdmin_noArgs(t *testing.T) {
	a, _ := newBundleApp(t)
	calls := stubElevation(t, false)
	if err := a.RelaunchAsAdmin(); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(*calls) != 1 || len((*calls)[0]) != 0 {
		t.Fatalf("relaunch args = %v; want one call with no args", *calls)
	}
	if err := (func() error { stubElevation(t, true); return a.RelaunchAsAdmin() })(); err == nil || err.Error() != "already_elevated" {
		t.Fatalf("elevated: err = %v", err)
	}
}
