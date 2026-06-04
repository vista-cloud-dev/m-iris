package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// minIRISYear is the oldest IRIS major (release year) m-iris supports.
const minIRISYear = 2022

// doctorCmd is `meta doctor` (driver-contract §5.7, plan §3): typed preflight
// diagnostics — the first thing CI and `m new` run. Each check is independent
// and self-describing ({name, ok, detail, fix}); the exit code lets CI branch:
// 0 all green, 6 engine-unreachable, 5 a check failed.
type doctorCmd struct{}

type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type doctorResult struct {
	Transport string        `json:"transport"`
	OK        bool          `json:"ok"`
	Checks    []doctorCheck `json:"checks"`
}

func (doctorCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := remoteOnly(conn); err != nil {
		return err
	}
	res, exit := runDoctorRemote(context.Background(), conn)
	if err := cc.Result(res, func() { renderDoctor(cc, res) }); err != nil {
		return err
	}
	switch exit {
	case clikit.ExitUnreachable:
		return clikit.Fail(clikit.ExitUnreachable, "UNREACHABLE",
			"engine unreachable — fix connectivity before other checks", "verify --base-url / network")
	case clikit.ExitRuntime:
		return clikit.Fail(clikit.ExitRuntime, "PREFLIGHT_FAILED",
			"one or more preflight checks failed", "see the failing checks above")
	}
	return nil
}

// runDoctorRemote runs the remote (Atelier) check matrix and returns the typed
// result plus the exit code (0 / 6 / 5).
func runDoctorRemote(ctx context.Context, conn *config.Conn) (doctorResult, int) {
	res := doctorResult{Transport: "remote"}
	add := func(name string, ok bool, detail, fix string) {
		res.Checks = append(res.Checks, doctorCheck{Name: name, OK: ok, Detail: detail, Fix: fix})
	}

	client, err := remoteClient(conn)
	if err != nil {
		// Missing base-url/namespace is a usage error, surfaced directly.
		add("config", false, err.Error(), "set --base-url and --namespace (or M_IRIS_* env)")
		return finalize(res), clikit.ExitRuntime
	}

	info, serr := client.ServerInfo(ctx)
	switch {
	case serr == nil:
		add("reachable", true, "Atelier root responded", "")
		add("auth", true, "credential accepted", "")
		add(versionOK(info.Version))
		add(namespaceCheck(conn.Namespace, info.Namespaces))
	case atelier.IsUnauthorized(serr):
		add("reachable", true, "server answered (HTTP 401)", "")
		add("auth", false, "authentication failed (HTTP 401) — bad or missing credential",
			"check --user/--password or --token-file")
		skipDownstream(add, "401")
		return finalize(res), clikit.ExitRuntime
	case atelier.IsForbidden(serr):
		add("reachable", true, "server answered (HTTP 403)", "")
		add("auth", false, "authenticated but forbidden (HTTP 403) — credential lacks privilege",
			"grant the user the Atelier/%Development role")
		skipDownstream(add, "403")
		return finalize(res), clikit.ExitRuntime
	default:
		add("reachable", false, "Atelier root unreachable: "+serr.Error(),
			"verify --base-url, port 52773, and network/TLS")
		skipDownstream(add, "unreachable")
		return finalize(res), clikit.ExitUnreachable
	}

	// query-privilege: the runner rides action/query, so prove that privilege now
	// (risk C7) rather than discovering it at first exec.
	add(queryPrivilegeCheck(ctx, client))

	// license is not probeable over Atelier (no endpoint); it needs ObjectScript
	// via the runner (M6). Report it honestly as not-probed rather than guessing.
	add("license", true, "not probed on remote (no Atelier license endpoint; checked via the runner in M6)", "")

	return finalize(res), exitFor(res)
}

func finalize(res doctorResult) doctorResult {
	res.OK = true
	for _, c := range res.Checks {
		if !c.OK {
			res.OK = false
			break
		}
	}
	return res
}

func exitFor(res doctorResult) int {
	for _, c := range res.Checks {
		if !c.OK {
			return clikit.ExitRuntime
		}
	}
	return clikit.ExitOK
}

// skipDownstream marks the checks that depend on a readable root as not-run.
func skipDownstream(add func(string, bool, string, string), why string) {
	for _, n := range []string{"version", "namespace", "query-privilege", "license"} {
		add(n, true, "skipped ("+why+")", "")
	}
}

var versionRe = regexp.MustCompile(`(\d{4})\.(\d+)`)

func versionOK(version string) (string, bool, string, string) {
	m := versionRe.FindStringSubmatch(version)
	if m == nil {
		return "version", true, "could not parse version " + strconv.Quote(version), ""
	}
	year, _ := strconv.Atoi(m[1])
	if year < minIRISYear {
		return "version", false,
			fmt.Sprintf("IRIS %s is older than the supported minimum %d.1", m[0], minIRISYear),
			"upgrade IRIS to a supported release"
	}
	return "version", true, "IRIS " + m[0], ""
}

func namespaceCheck(want string, namespaces []string) (string, bool, string, string) {
	if len(namespaces) == 0 {
		return "namespace", true, "server did not list namespaces; not verified", ""
	}
	for _, ns := range namespaces {
		if strings.EqualFold(ns, want) {
			return "namespace", true, "namespace " + want + " present", ""
		}
	}
	return "namespace", false, "namespace " + want + " not found on the server",
		"create the namespace, or target one of: " + strings.Join(namespaces, ", ")
}

func queryPrivilegeCheck(ctx context.Context, client *atelier.Client) (string, bool, string, string) {
	rows, err := client.Query(ctx, "SELECT 1 AS one")
	switch {
	case err == nil && len(rows) == 1 && rows[0]["one"] == "1":
		return "query-privilege", true, "action/query (SQL) usable — the remote runner can run", ""
	case atelier.IsForbidden(err):
		return "query-privilege", false, "no SQL/action-query privilege (HTTP 403)",
			"grant the user EXECUTE on the runner procedures / the %Development role"
	case err != nil:
		return "query-privilege", false, "action/query failed: " + err.Error(),
			"verify the user can run SQL via Atelier"
	default:
		return "query-privilege", false, "action/query returned no result", ""
	}
}

func renderDoctor(cc *clikit.Context, res doctorResult) {
	cc.Title("m-iris doctor — " + res.Transport)
	for _, c := range res.Checks {
		line := c.Name + ": " + c.Detail
		if c.OK {
			fmt.Fprintln(cc.Stdout, cc.Success(line))
		} else {
			fmt.Fprintln(cc.Stdout, cc.Failure(line))
			if c.Fix != "" {
				fmt.Fprintln(cc.Stdout, "    fix: "+c.Fix)
			}
		}
	}
}
