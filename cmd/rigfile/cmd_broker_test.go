package main

import (
	"strings"
	"testing"
)

func TestBrokerCommandsKeepTheChoiceAndReportStatus(t *testing.T) {
	m := newMachine(t)
	if r := m.run("", "broker", "status"); r.code != 1 || !strings.Contains(r.out, "Level 2: off") || !strings.Contains(r.out, "not running") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "enable"); r.code != 0 || !strings.Contains(r.out, "Level 2 is on") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "exclude", "legacy-tool"); r.code != 0 || !strings.Contains(r.out, "Level 1") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "status"); !strings.Contains(r.out, "Level 2: on") || !strings.Contains(r.out, "excluded: legacy-tool") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "include", "legacy-tool"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "status"); strings.Contains(r.out, "excluded") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "disable"); r.code != 0 || !strings.Contains(r.out, "Level 2 is off") {
		t.Fatalf("%+v", r)
	}
	for _, a := range [][]string{{"broker"}, {"broker", "nope"}, {"broker", "exclude"}} {
		if r := m.run("", a...); r.code != 2 {
			t.Fatalf("%v: %+v", a, r)
		}
	}
}
