package i18n

import (
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]Lang{
		"zh_CN.UTF-8": Zh,
		"zh-CN":       Zh,
		"en_US":       En,
		"C":           En,
		"fr_FR":       En,
	}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Fatalf("Parse(%q)=%q want %q", in, got, want)
		}
	}
}

func TestResolveMAWLang(t *testing.T) {
	t.Setenv("MAW_LANG", "zh")
	t.Setenv("LANG", "en_US.UTF-8")
	ResetForTest()
	if got := Resolve(); got != Zh {
		t.Fatalf("Resolve()=%q want zh", got)
	}
}

func TestTStatus(t *testing.T) {
	Set(En)
	if StatusLabel("ready") != "Ready" {
		t.Fatalf("en ready=%q", StatusLabel("ready"))
	}
	Set(Zh)
	if StatusLabel("ready") != "可领取" {
		t.Fatalf("zh ready=%q", StatusLabel("ready"))
	}
}

func TestE(t *testing.T) {
	Set(En)
	err := E("err.unknown_task", "next.task_list", "T-1")
	if err.Msg != "unknown task T-1" || err.Next != "maw task list" {
		t.Fatalf("%+v", err)
	}
}

func TestCatalogKeysSynced(t *testing.T) {
	for k := range catalogEn {
		if _, ok := catalogZh[k]; !ok {
			t.Errorf("zh missing key %q", k)
		}
	}
	for k := range catalogZh {
		if _, ok := catalogEn[k]; !ok {
			t.Errorf("en missing key %q", k)
		}
	}
}

func TestManagedKeysPresent(t *testing.T) {
	keys := []string{
		"cmd.managed.short", "err.managed_auto_off", "err.managed_platform",
		"err.managed_limit_required", "next.managed_explicit",
	}
	for _, k := range keys {
		if _, ok := catalogEn[k]; !ok {
			t.Fatalf("en missing %s", k)
		}
		if _, ok := catalogZh[k]; !ok {
			t.Fatalf("zh missing %s", k)
		}
	}
}
