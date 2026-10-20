package main

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"quartermaster/trials"
)

// store is the part of trials.Store this tool uses.
type store interface {
	Get(product, fingerprint string) (trials.Record, bool, error)
	Reset(product, fingerprint string) (int64, error)
	List(product string, limit int) ([]trials.Record, error)
}

var fingerprintRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

const usage = "usage: qm-trial [-db path] [-product POPF] show <fingerprint> | reset <fingerprint> | list [count]"

// run executes one command and writes its report to out. A usage error is
// returned as errUsage so main can exit with status 2.
func run(st store, product string, args []string, now time.Time, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "show", "reset":
		if len(args) != 2 {
			return errUsage
		}
		fp := strings.ToLower(strings.TrimSpace(args[1]))
		if !fingerprintRE.MatchString(fp) {
			return errors.New("fingerprint must be 64 hex characters")
		}
		if args[0] == "show" {
			return show(st, product, fp, now, out)
		}
		n, err := st.Reset(product, fp)
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Fprintf(out, "no %s trial for %s; nothing deleted\n", product, fp[:12])
			return nil
		}
		fmt.Fprintf(out, "deleted the %s trial for %s; that computer can start a new one\n", product, fp[:12])
		return nil
	case "list":
		limit := 20
		if len(args) > 2 {
			return errUsage
		}
		if len(args) == 2 {
			v, err := strconv.Atoi(args[1])
			if err != nil || v < 1 || v > 1000 {
				return errors.New("count must be 1 to 1000")
			}
			limit = v
		}
		recs, err := st.List(product, limit)
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			fmt.Fprintf(out, "no %s trials\n", product)
			return nil
		}
		for _, r := range recs {
			fmt.Fprintf(out, "%s  %s  %s  ends %s\n", r.Fingerprint[:12], status(r, now), stamp(r.StartedAt), stamp(r.EndsAt))
		}
		return nil
	}
	return errUsage
}

var errUsage = errors.New(usage)

func show(st store, product, fp string, now time.Time, out io.Writer) error {
	r, found, err := st.Get(product, fp)
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(out, "no %s trial for %s\n", product, fp[:12])
		return nil
	}
	fmt.Fprintf(out, "product:     %s\nfingerprint: %s\nstatus:      %s\nstarted:     %s\nends:        %s\nrecorded:    %s\n",
		r.Product, r.Fingerprint, status(r, now), stamp(r.StartedAt), stamp(r.EndsAt), stamp(r.CreatedAt))
	return nil
}

func status(r trials.Record, now time.Time) string {
	if now.Unix() >= r.EndsAt {
		return "ended  "
	}
	return "running"
}

func stamp(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04:05Z")
}
