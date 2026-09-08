# Protocol routing execution checkpoint

Plan: `.github/plans/protocol-routing.md`

- Implemented/reviewed/pushed feature commit fe7a2bbdf, integration4d3302acb525; exact upstreamrc35 integrated. UI729/root/RelayKit tests +185 fullapp checks passed; real SQLite3.50.4/PostgreSQL15.18/MySQL8.0.46 persistence and9 fresh/upgrade migration cases passed.
- Fixed old CI test synchronization in45e9a2b90; CI34196459950 allgreen.
- First release v1.0.0-rc.35-fork.20260908.t143823.g4d3302acb525 published (GHCR/binaries/Electron success). Server GHCR pull unauthorized; no GitHub credentials copied. Deployed local image using checksum-verified released Linux binary e8dc9702f544e198ea4eda53d701b2eccf35073cb048181fbd11f5bbd2eef9f3 on existing runtime base.
- First deployment applied39 policies; guards proved prices/abilities/routing policy/CTYun status1/unrelated settings unchanged. Snapshot `/opt/ops-backups/protocol-routing/20260908T065345250494Z.json`; previous protocol fields absent. Guard `/opt/ops-backups/protocol-routing/guard-before.json`.
- Real admin Responses probe succeeded200 completed+usage. Then user reported400 stateful/background onnativeMessages.
- Found and fixed context_management misclassified asstoredstate: new context_editing capability, exact nativeClaude/Responses preservation; safe/strict cross and nativeChat still rejectunsupportedpreservation; true previous_response_id/container/background blocked. Hotfix02312508e398 pushedmain. 199/199 E2E +fullGo/RelayKit/730frontend tests passed; independent scopedreview clear. E2E `/private/tmp/protocol-e2e/run-20260908-151956/results.json`.
- To restoreavailability temporarily disabled protocol_routing.enabled onall39 DBrows and restarted `new-api-protocol-canary` with first-release image. PUBLIC CADDY CURRENTLY POINTS TO new-api-protocol-canary:3000; it runs newcode withprotocolroutingdisabled, solegacyMessages worksandResponses temporarilyrevertsoldbehavior. RealMessages adminprobe withcontext_management succeeded200 end_turn.
- Primary `new-api-new-api-1` currentlyfirstrelease andnotreceivingnewtraffic; drainedfor>5minutes. Can safelyrecreateprimary withhotfiximage.
- Verified bearer-only native /v1/messages+context_management on Kimi,SenseNova,Volcengine (small tests). Updated local `/private/tmp/protocol-deployment-policies.json` to add nativeClaude endpoints+context_editing for SenseNova/Volcengine; Kimi existingClaude endpoint addsflag. Do not declarecontext_editing onChat (DTOlackfield). CTYun unchanged, no paidprobe.
- Hotfixrelease v1.0.0-rc.35-fork.20260908.t153045.g02312508e398 created; workflows GHCR34199958696,binaries34199962314,Electron34199965862. CurrentCI34199701617. Needverifyfinalstatuses.
- HotfixlocalstaticLinuxbinary built at `/private/tmp/protocol-hotfix-build/new-api`, SHA f4022beff21a92b6ec71e176ee177416f6cc26e05534096800d218526d5f7a29, uploaded andhash/versionverified at `/opt/ops-builds/protocol-routing/<hotfixversion>/new-api`. ServerlocalhotfixDockerbuild session11725 wasrunningatcheckpoint (basefirstrelease, preserveslicenses). Awaitcompletion beforedependentactions.

Remaining:
1. Verify hotfixlocalimagebuild/version. Replace override firstimage withnew-api:<hotfixversion>, recreate PRIMARY whilepubliccanaryserveslegacy.
2. Confirmprimaryhealthyhotfix; restoreCaddyto`new-api:3000`, reload. Thenapply UPDATEDprofilefile (scpnewJSONfirst; servergeneric `ops/protocol-routing-configure.py --apply`). Newprimaryreloadschannelcacheevery60seconds; oldcanarymayreloadprofilesitcan'tparse, butreceivesnonewrequests. Preferwaitcurrentrequestsdrainbeforeapplyifneeded; do notkillinflightstreams.
3. Aftercache refresh confirmreal nativeMessages+contextediting200 onnewprofile, Responses200 completed+usage, protocol_route adminmetadata matches. Preserveallguardhashes/CTYunstate. Recordactualtrafficerrorsandboundaries; do notdropcontextfield tofakecompatibility.
4. Wait300secondsafterCaddyreturntoprimarybeforestopping/removingoldcanary (allgrouprequestdeadline300s). Ensure only4 productioncontainersremain.
5. Updateexistingrunbook/verification runtimeversion, deploymentproof,CIstatus, registrylimitation; syncdocs/monitor scriptsserver; markplancompleteonlywhenactualdone. Commit/pushdocsonlyfollowup; noexistingtagrewrites. Localrootmaincanfast-forwardorigin/main.
6. Clean only local temporary containers protocol-test-postgres/protocol-test-mysql onceallDBchecksfinished; keepverificationartifacts. No furtherproviderprobesunlessnecessary; no credentials/bodyexports.

Use SSH taskcontrolsocket `-o ControlMaster=auto -o ControlPersist=60 -o ControlPath=/private/tmp/newapi-protocol-ssh-%C -o BatchMode=yes`; olddefaultmultiplexsocket occasionallyhangs andnewconnectionsoccasionallyreset. Checkmutationoutcomes; neverassumea failedfirstcommandexecutedbecausealatercommandreturned0.

## Latest checkpoint (supersedes temporary-state notes above)

Hotfix local image built and version/hash verified. Primary container is now hotfix g02312508e398. Caddy restored to primary at `hotfix_primary_restored_at` in server cutover.json (1788853461). Protocol routing still temporarily disabled in DB; wait old canary's300s drain, remove it, then scp+apply updated local `/private/tmp/protocol-deployment-policies.json` and allow channelcache60s refresh. This avoids old g4 canary seeing the new context_editing enum while serving an old request. Need verify nativeMessages+contextediting and Responses with protocol_route present after enable, not merely legacy-path200.

Hotfix workflows CI34199701617/GHCR34199958696/binaries34199962314/Electron34199965862 allsuccess. Source/standalone tests passed; frontend730 passed standalone. NativeMessages contextediting bearer-only provider tests succeeded forSenseNova,Volcengine,Kimi. Async optional userquestion asks client+model; noanswerneeded tocomplete verifiednativepathfix.


## Completed

Hotfix g02312508e398 is serving public traffic with39 corrected policies enabled. Canary removed after300s drain, original proxy hash restored; fourproductioncontainers healthy. Explicit realMessages+contextediting andResponses probes returned200/end_turn and200/completed with usage, and logged correctprotocolpaths. Prices/abilities/routing/CTYunstate/unrelatedsettings hashes unchanged. AllhotfixCI/releasejobs green. LocaltemporaryPG/MySQLcontainers removed; verificationartifacts retained. Existingrunbook/verification updated. RemainingSenseNova K3 rate-limit/emptySSE behaviors and unsupportedstored-state/count_tokens documented, not masked. No required implementation/deployment work remains for this phase.

## 2026-09-08 Codex capability incident — awaiting client reproduction

User reported `no verified protocol endpoint supports the request capabilities`. The 16:30/16:31 UTC+8 failures came from Windows Codex Desktop `/v1/responses`; historical logs contain no offending capability or model. Do not assume hosted search, images or a specific model. Asked the user asynchronously for application/model/time and then a retry with the expanded error; no reply received yet.

Diagnostic-only commit b2d9f9d2be3a deployed as v1.0.0-rc.35-fork.20260908.t163632.gb2d9f9d2be3a. Full service/relay/controller tests and CI34205449920 passed; binary34205491474, GHCR34205495519, Electron34205499743 all succeeded. Missing canonical capabilities are now returned per endpoint protocol without request values. Eligibility/configuration unchanged. Binary SHA5165b5732fcd9b958f4469508c8f1b74f0666bd9f87cf4627b72fbb0cfbd9d2b verified local/server.

Production primary/public use diagnostic version, Caddy restored, canary removed after zero active inbound and external upstream sockets were verified at1788857282. Four containers remain healthy/running. No prices, abilities, protocol settings or channel states changed. Snapshots/times in /opt/ops-backups/protocol-capability. New SSH control socket /private/tmp/newapi-capability-ssh-%C works; earlier protocol socket closed once, verified no mutation before retry. Next action: obtain new client error or observe matching application log, identify exact missing capability, then verify upstream/model/converter support before changing configuration or code. The client compatibility problem is not yet confirmed fixed.
