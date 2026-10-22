package trials

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

const v2DriverName = "sqlite"

func openV2DB(t *testing.T) (*sql.DB, *StoreV2) {
	t.Helper()
	db, err := sql.Open(v2DriverName, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := Open(db); err != nil { // v1 table, for PurgeLegacy
		t.Fatal(err)
	}
	st, err := OpenV2(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, st
}

func v2h(c byte) string { return strings.Repeat(string("0123456789abcdef"[c%16]), 64) }

func v2sg(kind string, c byte) Signal { return Signal{Kind: kind, Hash: v2h(c)} }

const v2Day = int64(86_400)

func TestFirstRunStartsATrialWithAllSignals(t *testing.T) {
	_, st := openV2DB(t)
	res, err := st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("serial", 2), v2sg("board", 3)}, 1000, 14)
	if err != nil {
		t.Fatal(err)
	}
	if res.Existed || res.Record.StartedAt != 1000 || res.Record.EndsAt != 1000+14*v2Day || res.Added != 3 {
		t.Fatalf("%+v", res)
	}
}

func TestSameHardwareGetsTheSameTrialBack(t *testing.T) {
	_, st := openV2DB(t)
	sigs := []Signal{v2sg("uuid", 1), v2sg("serial", 2), v2sg("board", 3)}
	first, _ := st.Start("POPF", sigs, 1000, 14)
	again, err := st.Start("POPF", sigs, 1000+5*v2Day, 14)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Existed || again.Record.ID != first.Record.ID || again.Record.StartedAt != 1000 || again.Added != 0 {
		t.Fatalf("%+v", again)
	}
}

func TestReinstallKeepsTheTrial(t *testing.T) {
	// A reinstall changes the OS but not the hardware signals; the client
	// sends the same set.
	_, st := openV2DB(t)
	sigs := []Signal{v2sg("uuid", 1), v2sg("serial", 2)}
	first, _ := st.Start("POPF", sigs, 1000, 14)
	again, _ := st.Start("POPF", sigs, 1000+30*v2Day, 14)
	if again.Record.ID != first.Record.ID || again.Record.EndsAt != first.Record.EndsAt {
		t.Fatalf("%+v vs %+v", again, first)
	}
}

func TestImagedComputersWithDifferentHardwareGetOwnTrials(t *testing.T) {
	_, st := openV2DB(t)
	a, _ := st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("serial", 2), v2sg("board", 3)}, 1000, 14)
	b, _ := st.Start("POPF", []Signal{v2sg("uuid", 4), v2sg("serial", 5), v2sg("board", 6)}, 1000+7*v2Day, 14)
	if a.Record.ID == b.Record.ID || b.Existed || b.Record.StartedAt != 1000+7*v2Day {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func TestReplacedPartStillMatchesAndIsLearned(t *testing.T) {
	_, st := openV2DB(t)
	first, _ := st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("serial", 2), v2sg("disk", 3)}, 1000, 14)
	// New disk: two signals unchanged.
	again, _ := st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("serial", 2), v2sg("disk", 9)}, 2000, 14)
	if again.Record.ID != first.Record.ID || again.Added != 1 {
		t.Fatalf("%+v", again)
	}
	// Later the board dies: the new disk alone still matches.
	later, _ := st.Start("POPF", []Signal{v2sg("uuid", 7), v2sg("disk", 9)}, 3000, 14)
	if later.Record.ID != first.Record.ID || later.Added != 1 {
		t.Fatalf("%+v", later)
	}
}

func TestConflictPicksMostMatchesThenEarliestAndNeverReassigns(t *testing.T) {
	db, st := openV2DB(t)
	a, _ := st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("serial", 2)}, 1000, 14)
	b, _ := st.Start("POPF", []Signal{v2sg("uuid", 3), v2sg("serial", 4)}, 2000, 14)
	// One signal from each, plus a new one: tie on matches -> earliest start (a).
	res, _ := st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("serial", 4), v2sg("disk", 8)}, 3000, 14)
	if !res.Conflict || res.Record.ID != a.Record.ID || res.Added != 1 {
		t.Fatalf("%+v", res)
	}
	var owner int64
	if err := db.QueryRow(`SELECT trial_id FROM trial_signals WHERE signal = ?`, v2h(4)).Scan(&owner); err != nil || owner != b.Record.ID {
		t.Fatalf("signal 4 moved to %d (%v)", owner, err)
	}
	// Two signals of b against one of a: b wins.
	res, _ = st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("uuid", 3), v2sg("serial", 4)}, 4000, 14)
	if res.Record.ID != b.Record.ID || !res.Conflict {
		t.Fatalf("%+v", res)
	}
}

func TestSignalCapPerTrial(t *testing.T) {
	_, st := openV2DB(t)
	first, _ := st.Start("POPF", []Signal{v2sg("uuid", 1)}, 1000, 14)
	for i := byte(2); i < 40; i++ {
		_, err := st.Start("POPF", []Signal{v2sg("uuid", 1), {Kind: "disk", Hash: strings.Repeat("a", 62) + string("0123456789abcdef"[i%16]) + string("0123456789abcdef"[i/16])}}, 2000, 14)
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ := st.GetV2(first.Record.ID)
	if got.Signals != MaxSignalsPerTrial {
		t.Fatalf("signals = %d", got.Signals)
	}
}

func TestProductsAreSeparate(t *testing.T) {
	_, st := openV2DB(t)
	a, _ := st.Start("POPF", []Signal{v2sg("uuid", 1)}, 1000, 14)
	b, err := st.Start("LAMP", []Signal{v2sg("uuid", 1)}, 1000, 14)
	if err != nil || b.Existed || b.Record.ID == a.Record.ID {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestResetLetsTheComputerStartOver(t *testing.T) {
	db, st := openV2DB(t)
	sigs := []Signal{v2sg("uuid", 1), v2sg("serial", 2)}
	first, _ := st.Start("POPF", sigs, 1000, 14)
	n, err := st.ResetV2("POPF", first.Record.ID)
	if err != nil || n != 1 {
		t.Fatalf("reset %d %v", n, err)
	}
	var left int
	db.QueryRow(`SELECT COUNT(*) FROM trial_signals`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d signals left", left)
	}
	again, _ := st.Start("POPF", sigs, 5000, 14)
	if again.Existed || again.Record.StartedAt != 5000 {
		t.Fatalf("%+v", again)
	}
	if n, _ := st.ResetV2("POPF", 9999); n != 0 {
		t.Fatalf("reset of a missing id deleted %d", n)
	}
}

func TestOrphanSignalsDoNotBlockANewTrial(t *testing.T) {
	db, st := openV2DB(t)
	first, _ := st.Start("POPF", []Signal{v2sg("uuid", 1)}, 1000, 14)
	// A trial deleted by hand leaves its signals behind.
	db.Exec(`DELETE FROM trials_v2 WHERE id = ?`, first.Record.ID)
	again, err := st.Start("POPF", []Signal{v2sg("uuid", 1)}, 2000, 14)
	if err != nil || again.Existed || again.Record.StartedAt != 2000 {
		t.Fatalf("%+v %v", again, err)
	}
	var owner int64
	db.QueryRow(`SELECT trial_id FROM trial_signals WHERE signal = ?`, v2h(1)).Scan(&owner)
	if owner != again.Record.ID {
		t.Fatalf("owner %d", owner)
	}
}

func TestConcurrentFirstRunsMakeOneTrial(t *testing.T) {
	_, st := openV2DB(t)
	sigs := []Signal{v2sg("uuid", 1), v2sg("serial", 2)}
	var wg sync.WaitGroup
	ids := make(chan int64, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := st.Start("POPF", sigs, 1000, 14)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- res.Record.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[int64]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("%d different trials", len(seen))
	}
}

func TestListShowsNewestFirstWithSignalCounts(t *testing.T) {
	_, st := openV2DB(t)
	st.Start("POPF", []Signal{v2sg("uuid", 1), v2sg("serial", 2)}, 1000, 14)
	st.Start("POPF", []Signal{v2sg("uuid", 3)}, 2000, 14)
	list, err := st.ListV2("POPF", 10)
	if err != nil || len(list) != 2 || list[0].StartedAt != 2000 || list[0].Signals != 1 || list[1].Signals != 2 {
		t.Fatalf("%+v %v", list, err)
	}
}

func TestPurgeLegacyDeletesOnlyV1(t *testing.T) {
	db, st := openV2DB(t)
	v1, _ := Open(db)
	v1.Start("POPF", strings.Repeat("a", 64), 1, 2, 1)
	v1.Start("POPF", strings.Repeat("b", 64), 1, 2, 1)
	st.Start("POPF", []Signal{v2sg("uuid", 1)}, 1000, 14)
	n, err := st.PurgeLegacy("POPF")
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if list, _ := st.ListV2("POPF", 5); len(list) != 1 {
		t.Fatal("v2 trial was purged")
	}
}

func TestPickOwner(t *testing.T) {
	best, conflict := pickOwner([]candidate{{ID: 5, Matches: 1, StartedAt: 10}, {ID: 2, Matches: 1, StartedAt: 10}, {ID: 9, Matches: 1, StartedAt: 3}})
	if best.ID != 9 || !conflict {
		t.Fatalf("%+v %v", best, conflict)
	}
	best, conflict = pickOwner([]candidate{{ID: 5, Matches: 2, StartedAt: 99}, {ID: 2, Matches: 1, StartedAt: 1}})
	if best.ID != 5 || !conflict {
		t.Fatalf("%+v", best)
	}
	best, conflict = pickOwner([]candidate{{ID: 4, Matches: 1, StartedAt: 1}, {ID: 2, Matches: 1, StartedAt: 1}})
	if best.ID != 2 {
		t.Fatalf("%+v", best)
	}
	if _, c := pickOwner(nil); c {
		t.Fatal("conflict with no candidates")
	}
	if _, c := pickOwner([]candidate{{ID: 1}}); c {
		t.Fatal("one candidate is not a conflict")
	}
}

func TestValidateSignals(t *testing.T) {
	good := []Signal{{"uuid", strings.ToUpper(v2h(1))}, {"serial", v2h(2)}}
	out, err := ValidateSignals(good)
	if err != nil || out[0].Hash != v2h(1) {
		t.Fatalf("%+v %v", out, err)
	}
	bad := map[string][]Signal{
		"empty":        nil,
		"too many":     {v2sg("disk", 1), v2sg("disk", 2), v2sg("disk", 3), v2sg("disk", 4), v2sg("disk", 5), v2sg("disk", 6), v2sg("disk", 7), v2sg("disk", 8), v2sg("disk", 9)},
		"unknown kind": {{"mac", v2h(1)}},
		"short hash":   {{"uuid", "abcd"}},
		"non hex":      {{"uuid", strings.Repeat("g", 64)}},
		"repeat":       {v2sg("uuid", 1), v2sg("serial", 1)},
		"os with more": {v2sg("os", 1), v2sg("uuid", 2)},
	}
	for name, in := range bad {
		if _, err := ValidateSignals(in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := ValidateSignals([]Signal{v2sg("os", 1)}); err != nil {
		t.Errorf("a lone os signal rejected: %v", err)
	}
}
