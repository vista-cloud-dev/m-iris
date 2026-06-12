/// m.iris.Runner is the remote execution substrate for the m-iris driver.
/// (Package "m.iris" — NOT "m_iris": IRIS class names forbid underscores; the
/// package's dots map to the SQL schema "m_iris", so the SqlProc names below
/// stay m_iris.*.)
///
/// Atelier exposes no raw "run ObjectScript" endpoint, and Go has no official
/// IRIS Native SDK — so on the `remote` transport ALL ObjectScript (exec, data,
/// cover, admin) rides this class: its methods are projected as SQL procedures,
/// invoked via POST .../action/query, and their results are read back out of a
/// result global. This one class is therefore the entire remote substrate
/// (driver-plan §5 task 8, risk B2). It is a security boundary, so every entry
/// point is role-gated AND parameterized (callers bind values, never concatenate).
///
/// NB: executable lines avoid spaces after commas — ObjectScript treats a space
/// inside a command argument as a terminator (a space after a comma in a QUIT
/// expression triggers #1043 "QUIT argument not allowed").
///
/// Result global, keyed by a caller-supplied run id (rid):
///   ^mIrisRun(rid,"status") = 0 ok | 5 fault | 7 unauthorized
///   ^mIrisRun(rid,"error")  = "mnemonic|routine|line|text"   (on fault, contract §7)
Class m.iris.Runner Extends %RegisteredObject
{

/// authorized reports whether the caller may drive the substrate. Holding the
/// m_iris_runner role is the gate; _SYSTEM is allowed so a fresh instance works
/// before the role is provisioned. At deploy, SQL EXECUTE on these procedures is
/// granted only to m_iris_runner (defense in depth: app-role AND SQL privilege).
ClassMethod authorized() As %Boolean [ Private ]
{
	quit ($SYSTEM.Security.Check("m_iris_runner","USE")'="")||($USERNAME="_SYSTEM")
}

/// fault records a caught exception into the result global in contract-§7 shape
/// (mnemonic|routine|line|text) and returns status 5.
ClassMethod fault(rid As %String, ex As %Exception.AbstractException) As %Integer [ Private ]
{
	set loc=ex.Location
	set routine=$piece(loc,"^",2)
	set line=$piece($piece(loc,"^",1),"+",2)
	set ^mIrisRun(rid,"status")=5
	set ^mIrisRun(rid,"error")=ex.Name_"|"_routine_"|"_line_"|"_ex.DisplayString()
	quit 5
}

/// RunRef executes entryref `ref` (e.g. "RUN^STDHARN"). The entryref owns any
/// payload it writes; this method only drives execution and traps faults.
/// SQL:  SELECT m_iris.RunRef(?,?,?)
ClassMethod RunRef(rid As %String, ref As %String, args As %String = "") As %Integer [ SqlName = "RunRef", SqlProc ]
{
	if '..authorized() quit 7
	kill ^mIrisRun(rid)
	set ^mIrisRun(rid,"status")=0
	try {
		do start^mIrisIO(rid)
		do @ref
		do stop^mIrisIO()
	} catch ex {
		do stop^mIrisIO()
		do ..fault(rid,ex)
	}
	// "done" lets the caller recover the outcome from ^mIrisRun even when this
	// SqlProc's HTTP response body is lost — a KIDS install can corrupt the
	// response device so the action/query returns an empty body, but the run's
	// status/out/error are already committed to the global here.
	set ^mIrisRun(rid,"done")=1
	quit ^mIrisRun(rid,"status")
}

/// Eval executes one ObjectScript command line via XECUTE (contract exec.eval).
/// SQL:  SELECT m_iris.Eval(?,?)
ClassMethod Eval(rid As %String, cmd As %String) As %Integer [ SqlName = "Eval", SqlProc ]
{
	if '..authorized() quit 7
	kill ^mIrisRun(rid)
	set ^mIrisRun(rid,"status")=0
	try {
		do start^mIrisIO(rid)
		xecute cmd
		do stop^mIrisIO()
	} catch ex {
		do stop^mIrisIO()
		do ..fault(rid,ex)
	}
	set ^mIrisRun(rid,"done")=1
	quit ^mIrisRun(rid,"status")
}

/// GetOut returns ^mIrisRun(rid,"out") Base64-encoded so a runner result that
/// contains control bytes — a KIDS install's ANSI/terminal output (ESC, CR, page
/// controls) — survives the action/query JSON transport intact. A raw read of
/// such a value truncates/mangles at the first non-text byte, dropping the
/// trailing result markers. (contract: Stdout = runner result-global text.)
/// SQL:  SELECT m_iris.GetOut(?)
ClassMethod GetOut(rid As %String) As %String [ SqlName = "GetOut", SqlProc ]
{
	if '..authorized() quit ""
	quit $system.Encryption.Base64Encode($get(^mIrisRun(rid,"out")))
}

/// GetGlobal returns $get(@ref); ref is a full global reference, e.g.
/// "^mIrisRun(""r1"",""status"")". (contract data.get + result-global reads)
/// SQL:  SELECT m_iris.GetGlobal(?)
ClassMethod GetGlobal(ref As %String) As %String [ SqlName = "GetGlobal", SqlProc ]
{
	if '..authorized() quit ""
	quit $get(@ref)
}

/// SetGlobal sets @ref=value (contract data.set, fixture seeding).
/// SQL:  SELECT m_iris.SetGlobal(?,?)
ClassMethod SetGlobal(ref As %String, value As %String = "") As %Integer [ SqlName = "SetGlobal", SqlProc ]
{
	if '..authorized() quit 7
	set @ref=value
	quit 1
}

/// KillGlobal kills @ref (contract data.kill; ephemeral-prefix teardown).
/// SQL:  SELECT m_iris.KillGlobal(?)
ClassMethod KillGlobal(ref As %String) As %Integer [ SqlName = "KillGlobal", SqlProc ]
{
	if '..authorized() quit 7
	kill @ref
	quit 1
}

/// Ping returns $zversion — a cheap readiness/version probe through the same
/// substrate, so `doctor` can prove the action/query privilege, not just
/// reachability (risks C3, C7).
/// SQL:  SELECT m_iris.Ping()
ClassMethod Ping() As %String [ SqlName = "Ping", SqlProc ]
{
	quit $zversion
}

}
