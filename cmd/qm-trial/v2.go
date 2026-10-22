package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"quartermaster/trials"
)

// storeV2 is the part of trials.StoreV2 the v2 commands use.
type storeV2 interface {
	GetV2(id int64) (trials.RecordV2, bool, error)
	ListV2(product string, limit int) ([]trials.RecordV2, error)
	ResetV2(product string, id int64) (int64, error)
	CountLegacy(product string) (int64, error)
	PurgeLegacy(product string) (int64, error)
}

const usageV2 = `v2 trials (hardware-matched, 0.1.12 and later):
  qm-trial list2 [count]        newest trials with their signal counts
  qm-trial show2 <id>           one trial
  qm-trial reset2 <id>          delete one trial and its signals (that computer can start over)
  qm-trial purge-v1 [--yes]     delete ALL v1 (fingerprint) trials; without --yes only counts them`

func isV2Command(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "list2", "show2", "reset2", "purge-v1", "help":
		return true
	}
	return false
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("id must be a positive number (see list2)")
	}
	return id, nil
}

func statusV2(r trials.RecordV2, now time.Time) string {
	if now.Unix() >= r.EndsAt {
		return "ended  "
	}
	return "running"
}

// runV2 executes one v2 command and writes its report to out.
func runV2(st storeV2, product string, args []string, now time.Time, out io.Writer) error {
	switch args[0] {
	case "help":
		fmt.Fprintln(out, usage)
		fmt.Fprintln(out, usageV2)
		return nil
	case "list2":
		limit := 20
		if len(args) > 2 {
			return errors.New(usageV2)
		}
		if len(args) == 2 {
			v, err := strconv.Atoi(args[1])
			if err != nil || v < 1 || v > 1000 {
				return errors.New("count must be 1 to 1000")
			}
			limit = v
		}
		recs, err := st.ListV2(product, limit)
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			fmt.Fprintf(out, "no %s v2 trials\n", product)
			return nil
		}
		for _, r := range recs {
			fmt.Fprintf(out, "%5d  %s  %s  ends %s  %d signals\n", r.ID, statusV2(r, now), stamp(r.StartedAt), stamp(r.EndsAt), r.Signals)
		}
		return nil
	case "show2", "reset2":
		if len(args) != 2 {
			return errors.New(usageV2)
		}
		id, err := parseID(args[1])
		if err != nil {
			return err
		}
		if args[0] == "show2" {
			r, found, err := st.GetV2(id)
			if err != nil {
				return err
			}
			if !found || r.Product != product {
				fmt.Fprintf(out, "no %s v2 trial %d\n", product, id)
				return nil
			}
			fmt.Fprintf(out, "id:        %d\nproduct:   %s\nstatus:    %s\nstarted:   %s\nends:      %s\nrecorded:  %s\nsignals:   %d\n",
				r.ID, r.Product, statusV2(r, now), stamp(r.StartedAt), stamp(r.EndsAt), stamp(r.CreatedAt), r.Signals)
			return nil
		}
		n, err := st.ResetV2(product, id)
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Fprintf(out, "no %s v2 trial %d; nothing deleted\n", product, id)
			return nil
		}
		fmt.Fprintf(out, "deleted %s v2 trial %d; that computer can start a new one\n", product, id)
		return nil
	case "purge-v1":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--yes") {
			return errors.New(usageV2)
		}
		count, err := st.CountLegacy(product)
		if err != nil {
			return err
		}
		if len(args) == 1 {
			fmt.Fprintf(out, "%d v1 %s trials would be deleted; run again with --yes to delete them\n", count, product)
			return nil
		}
		n, err := st.PurgeLegacy(product)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "deleted %d v1 %s trials\n", n, product)
		return nil
	}
	return errors.New(usageV2)
}
