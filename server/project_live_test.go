package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestProjectLivePreservesNativeTargetsAndDeduplicatesOwnedEvents(t *testing.T) {
	p, item := plannerFixture(t, "normal")
	run := projectRun{ID: "live", Role: "plan", ThreadID: "thread", Status: "running", CreatedAt: time.Now()}
	if err := p.saveRun(item.ID, &run); err != nil {
		t.Fatal(err)
	}
	doc, err := validatePlan(item.Root, samplePlan())
	if err != nil {
		t.Fatal(err)
	}
	if err = p.complete(context.Background(), item.ID, &run, &doc); err != nil {
		t.Fatal(err)
	}
	native := activity{ID: "native", Kind: "read", Status: "completed", At: time.Now(), Mapping: "exact", Targets: []activityTarget{{Group: "App", Key: "native-feature", Precision: "symbol"}}}
	owned := activity{ID: "owned", ThreadID: checksum("thread"), Kind: "read", Status: "completed", At: time.Now(), Mapping: "unmapped", Paths: []string{"main.go"}, Locations: []activityLocation{{Path: "main.go", Line: 3, EndLine: 3}}}
	stored, _ := json.Marshal(storedProjectActivity{Event: owned, Proof: []string{" println(1)\n"}})
	if _, err = p.db.Exec(`INSERT INTO project_run_events VALUES (?,?,?,?)`, run.ID, "owned", owned.At.Format(time.RFC3339Nano), string(stored)); err != nil {
		t.Fatal(err)
	}
	stale := owned
	stale.Status = "running"
	base, _ := json.Marshal(snapshot{Connection: "ready", Events: []activity{native, stale}})
	var got snapshot
	if err = json.Unmarshal(p.live(item, base), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 {
		t.Fatalf("duplicate events: %+v", got.Events)
	}
	for _, e := range got.Events {
		if e.ID == "native" && (len(e.Targets) != 1 || e.Targets[0].Key != "native-feature" || e.Mapping != "exact") {
			t.Fatal("lost native precise target")
		}
		if e.ID == "owned" && (e.Status != "completed" || len(e.Targets) != 1 || e.Targets[0].Precision != "symbol" || e.Targets[0].Key != "main") {
			t.Fatalf("owned proof or completion was lost: %+v", e)
		}
	}
	if len(got.Sessions) != 1 || got.SessionCount != 0 || got.Sessions[0].Status != "idle" {
		t.Fatalf("terminal run still busy: %+v", got.Sessions)
	}
}
