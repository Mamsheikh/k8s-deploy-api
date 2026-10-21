# Local Kubernetes deployment API plan

Build the application lifecycle in nine small PRs for a developer learning Kubernetes through Go.
Keep Kubernetes as the source of truth and independently compare API behavior with kubectl.
Deliver PR-01 through PR-09 in order. Stop each owner at a verified head before starting the next.
This plan replaces the older task draft. [The specification](spec.md) remains the source of product requirements.
Planning changed documentation only. Task 01 source and local functional verification now exist. See [the runbook](runbook.md#verification-status) for the checks that passed and the checks still blocked.

## How to read this

One box is one unit of work. Every box names the evidence that checks it. Check a box only when its evidence exists, such as a log, screenshot, test result, or SHA. The body is a how-to. The appendices explain and record.

The program runs `skills/poteto-mode/playbooks/autopilot-stack.md` under the installed plugin, together with `autopilot-full.md` where the stack playbook delegates to it. Every PR belongs to the operator at landing. Planning does not authorize implementation, publishing, merging, or a running audit cadence.

Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

The required stack is `PR-01 -> PR-02 -> PR-03 -> PR-04 -> PR-05 -> PR-06 -> PR-07 -> PR-08 -> PR-09`. Logical dependencies appear in each section. Later PRs may carry earlier stack commits but their reviewed diff covers only their own change.

## Program checklist

### Arm the program

- [ ] State the protocol and this plan to the operator, then stop. Start execution only on the operator's explicit go.
- [ ] Read these from the installed plugin at program start. Re-read them at every tick.
  - [ ] `skills/poteto-mode/playbooks/autopilot-stack.md`
  - [ ] `skills/poteto-mode/playbooks/autopilot-full.md`
  - [ ] `skills/poteto-mode/playbooks/shipping.md`
  - [ ] `skills/swarm/SKILL.md`
  - [ ] `skills/poteto-mode/references/codex-tools.md`
  - [ ] `skills/poteto-mode/playbooks/opening-a-pr.md`
  - [ ] `skills/principle-sequence-verifiable-units/SKILL.md`
- [ ] On the operator's go, arm the hourly audit tick as a real cadence with the tick prompt below. On Codex, use a supported scheduled task or a retained supervised cadence and record its handle. Do not claim a cadence is armed without that evidence.
- [ ] Use this tick prompt, verbatim. "Re-read the execution playbook from the installed plugin. Audit the operation against it and fix drift in this tick. Probe every active lane and judge progress by side effects only. Stand down a lane only on affirmative failure evidence, and dispatch its replacement in the same tick. Then post a short status message to the operator in chat only when the audit found a tracked change that no earlier status message reported, such as a PR opened, a code-ready head, a round launched or closed, a verdict, a merge, a stuck agent and the action taken, a blocker added or cleared, or a decision only the operator can make. Name every such change and nothing else. Do not repeat a table, the merged list, or an unchanged blocker. If the audit found none, end the turn with no reply text. Either way, log this tick's row in your decision trail. The row names the items reported, or none."
- [ ] On the operator's hold or stand-down, send every owner a zero-writes order at once.

### Spawn owners

- [ ] Spawn one owner per PR with the full lifecycle the execution playbook names. Run one implementation owner at a time for this learning project. Parallelize independent read-only verification after the owner stops writing.
- [ ] Follow this dependency graph. Start dependent work only after its parent merges, or rebase its branch onto the parent's exact tip when the execution playbook stacks. A same-repository child PR targets its parent branch. A fork child PR targets trunk while retaining local parent ancestry. Freeze the bottom-to-top order because fork PR bases do not encode it.
  - [ ] PR-01 starts from the verified trunk commit.
  - [ ] PR-02 follows PR-01. PR-03 follows PR-02. PR-04 follows PR-03.
  - [ ] PR-05 requires PR-03 and follows PR-04 in the delivery stack. PR-06 follows PR-05.
  - [ ] PR-07 requires PR-04 and PR-06. PR-08 requires PR-05 and follows PR-07 in the delivery stack.
  - [ ] PR-09 requires PR-04 and PR-06 and follows PR-08 in the delivery stack.
- [ ] Hold the file boundaries in each PR below. Proposed paths do not exist yet. Record any justified path adjustment before editing. Keep one writer per branch.
- [ ] Hold the review gate. PR-01 through PR-09 change observable API behavior. They wait for the operator's review in chat with screenshots and a video before merge.

### PR mechanics, for every PR

- [ ] Resolve the forge once. Default to `gh`; if `command -v origin` succeeds and Origin can resolve the repository, use `origin pr` for every PR operation. Record any fallback to `gh`. Record the intended PR base repository as canonical `<base-repo>` and validate it through the active forge. Do not infer it from the checkout's default remote. Capture it as a shell variable and pass `--repo "$base_repo"` to every `gh pr` command. Resolve and validate `<head-url>` through Shipping step 1 and capture it as `head_url` for the live-lane fetch. When the head repository is a fork, validate its identity and record its owner and repository name as `<fork-owner>` and `<head-name>`. Never require `gt`.
- [ ] Open the PR before self-proof and follow the readiness rule in `skills/poteto-mode/playbooks/opening-a-pr.md`. Open it ready by default. When repository instructions require a draft until named evidence exists, keep it draft until that evidence is recorded. Use `origin pr create --status open --base "$base_branch"` or `gh pr create --base "$base_branch" --repo "$base_repo"` for a ready same-repository PR. A same-repository stack child targets its parent branch. Every fork PR targets trunk. With GitHub, capture the approved PR title and body as `<title>` and `<body>`, then create it with `gh api --method POST "repos/$base_repo/pulls" -f "title=$title" -f "body=$body" -f "head=$fork_owner:$branch" -f "head_repo=$head_name" -f "base=$trunk" --jq .html_url`; add `-F draft=true` when repository instructions require a draft. Otherwise use the resolved Origin command. Stacked fork branches retain local parent ancestry.
- [ ] Run `go vet ./...`, `go test ./...`, and `go build ./...` before the PR-facing push. Check `gofmt -l` on changed Go files. Run `go test -race ./...` for streaming and final lifecycle checks. Follow additional repository gates if later introduced. Push with hooks on.
- [ ] Run `/deslop` before each commit and `/no-comments` before review.
- [ ] Triage every Bugbot and security-reviewer comment per `skills/poteto-mode/references/bugbot-triage.md` under the installed plugin.
- [ ] Only the root rebases or retargets stack links. Owners push only their own branch and never change its base. Before rewriting, follow Shipping disarm-and-confirm rules for the link and descendants. Record the old patch base, published SHA, exact new parent tip, and explicit old-base-to-parent move. After a parent lands, use Shipping's old-base-to-trunk move. Recheck CI and patch ID at every rewritten head.

### Verdict and merge, for every PR

- [ ] A full swarm runs once, at the code-ready head. Another full swarm runs only for new behavior, a redesign, or a large new diff. A rebase, a merge-conflict resolution, or a minor fix gets a single verifier at the new head. Run each full swarm per `skills/swarm/SKILL.md`. One gates lane. The ten live lanes from the PR's **Verify, live** block. The perf lane from its **Verify, perf** block. Two or more audit lanes, each with its own focus, that read the diff and the receipts and distrust the PR body. The root audits the receipts in the merge-ready report before the verdict.
- [ ] Clean only when every lane is `PASS`. Findings go back to the owner, including a defect that a lane filed as a note. A new head gets a fresh verdict, except for results that stay valid under the patch ID rule in `skills/poteto-mode/playbooks/shipping.md`.
- [ ] Append only clean PRs to the frozen PR-01 through PR-09 stack under Autopilot-stack. Record verdict SHA, current landing SHA, recorded patch base, and patch ID. Only the root changes stack topology. The operator lands one bottom PR at a time through Shipping. Preserve a verdict only when Shipping permits the unchanged patch ID and CI passes at the landing SHA.

### Boot recipe, for every live lane

Each live lane is one `swarm workers` lane at the PR head, resolved through provider dispatch, in its own worktree or output directory, with its own receipt. Drive the surface only through the driver skill this plan names.

- [ ] `git fetch -- "$head_url" "refs/heads/$head_branch" && git checkout --detach "$head_sha"` in the lane's worktree.
- [ ] Read Appendix D, select context `docker-desktop`, and confirm namespace `deploy-api` exists. Run `go run .` and wait for `curl --fail http://127.0.0.1:8080/healthz`. Startup-failure scenarios instead require no listener. Save the actual command, stdout, stderr, exit code, SHA, and provider receipt.
- [ ] Drive the API with shell commands using `curl`, and compare with `kubectl --context=docker-desktop -n deploy-api get`, `describe`, `logs`, and `rollout status`. Use port-forward only for application access. Use the shell driver mapping in `skills/poteto-mode/references/codex-tools.md` on Codex. Screenshots show the terminal and accompany raw logs, never replace them.
- [ ] Save every screenshot to `.scratch/deployment-api/swarm-PR-NN/worker-<n>/<slug>.png` and return the paths with the receipt path.

Only one real API process can bind `127.0.0.1:8080`. Serialize all real-process lanes and trunk/head probes. Controlled-upstream httptest lanes use ephemeral ports and can run concurrently. Stop each process and its forwards before the next lane.

Use isolated fixture names `pNNwN-a` and `pNNwN-b` for each lane. Substitute them for `learn-a` and `learn-b` in these recipes. Kubernetes mutations remain in `deploy-api`. Never delete a resource unless its ownership and fixture identity have been checked. Clean up each lane before the next if the cluster cannot accommodate multiple retained fixture sets. Distinct receipts still cover all ten scenarios.

For a controlled upstream scenario, drive the real HTTP server through `httptest.Server` and a controllable Kubernetes HTTP server. Save a test transcript and a terminal screenshot. Mark this as transport or application evidence. Never label it live Kubernetes evidence. The remaining cluster lanes prove actual controller behavior.

At each PR boundary, record the throughput checkpoint in its receipt. Name the smallest unverified behavior, the command that proves it, and any setup cost that makes the next PR too large. Split the next PR before implementation if it cannot reach a checkable state in one review.

## Start a read-only Kubernetes connection and health endpoint (PR-01)

Local unit, race, build, vet, live startup, restart, and absolute timing checks passed on 2026-10-21. See [the verification record](runbook.md#verification-status). Formal PR and media-review gates remain open.

**Depends on.** None.

**Files.**

- [x] Create or edit `go.mod`.
- [x] Create or edit `go.sum`.
- [x] Create or edit `main.go`.
- [x] Create `main_test.go` to verify startup gates serving.
- [x] Create or edit `internal/kube/client.go`.
- [x] Create or edit `internal/kube/client_test.go`.
- [x] Create or edit `internal/httpapi/server.go`.
- [x] Create or edit `internal/httpapi/server_test.go`.
- [x] Create or edit `docs/runbook.md`.

**Build.**

- [x] Initialize a Go module. Recheck the environment and select an available exact v0.34.x patch for all three Kubernetes modules. Record the selected versions in go.mod and the runbook.
- [x] Load explicit `-kubeconfig` or normal client-go loading rules in `internal/kube/client.go`. Always select `docker-desktop` without changing the current kubeconfig context.
- [x] Fetch version and namespace under one overall 10-second context before listening on `127.0.0.1:8080`. Expose only `GET /healthz` with JSON `{"status":"ok"}`. Exclude workload access, mutation, and periodic checks.

**You see.**

- [x] Health returns 200 after successful startup. Bad configuration and failed checks exit before opening the listener. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [x] Test health routing and JSON headers with httptest. Test explicit-file failure without fallback, missing context, version/namespace failures, Forbidden, shared cancellation, and no workload writes.
- [x] Use a controlled HTTP upstream to prove discovery and namespace GET share one deadline. Use short injected deadlines. Default tests never read real kubeconfig.
- [x] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Run the health/startup scenario on trunk and head. Record that trunk has no executable if so. Gate successful head health and no workload writes. Save `pr-01-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. Start against Docker Desktop and GET /healthz. Compare version and namespace with kubectl. Save `pr-01-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. Compare workload inventory before and after startup. Also inspect the controlled-upstream request log for read-only intent. Save `pr-01-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Run `go run . -kubeconfig /nonexistent/deploy-api-kubeconfig`. Check nonzero exit and no listener. Save `pr-01-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. Pass an invalid YAML file created only in the lane output directory. Check parse failure without fallback. Save `pr-01-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Supply a temporary kubeconfig with no docker-desktop context. Check clear nonzero failure. Save `pr-01-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Use a controlled upstream that returns namespace NotFound. Check no listener. Save `pr-01-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Use a controlled upstream that returns Forbidden. Check no credentials in the error. Save `pr-01-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Delay version and namespace responses in a controlled upstream. Check the combined deadline cancels both operations. Save `pr-01-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Stop the API, restart it, and GET health. Check port release and unchanged workload inventory. Save `pr-01-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Kubernetes startup-check elapsed time and health request elapsed time.
- [ ] Probe. Build first, then record 20 startup checks and 100 health requests per side where executable. Interleave trunk and head. Exclude compilation and downloads. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if a Kubernetes startup check exceeds 10 seconds plus 1 second of scheduling tolerance, or local health p95 exceeds 250 ms. If trunk implements the same scenario, also fail above max(1.2 times trunk p95, trunk p95 plus 50 ms). These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-01-review-regression.png` and `.scratch/deployment-api/media/pr-01-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-01-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-01 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Create a named Deployment and Service (PR-02)

Source and local unit, race, live creation, Service access, and acceptance-budget checks now exist. See [the runbook](runbook.md#pr-02-verification-status). The formal independent swarm and media-review gates remain open. Local checks do not replace those gates.

**Depends on.** PR-01.

**Files.**

- [ ] Create or edit `internal/httpapi/apps.go`.
- [ ] Create or edit `internal/httpapi/json.go`.
- [ ] Create or edit `internal/httpapi/errors.go`.
- [ ] Create or edit `internal/httpapi/apps_test.go`.
- [ ] Create or edit `internal/kube/apps.go`.
- [ ] Create or edit `internal/kube/resources.go`.
- [ ] Create or edit `internal/kube/apps_test.go`.
- [ ] Create or edit `docs/runbook.md`.

**Build.**

- [ ] Define CreateAppRequest, resource outcomes, and the JSON error envelope. Decode one JSON object and reject unknown fields or trailing data. Validate the spec's 1 through 63 character name rule, nonempty image, and integer port 1 through 65535.
- [ ] Implement POST /apps. Create Deployment then Service under one ordinary 10-second operation budget. Do not wait for readiness. Map malformed input to 400, absence to 404, conflicts to 409, upstream unavailability or denial to 503, upstream deadline to 504, and unexpected errors to 500. Preserve useful reasons without credentials.
- [ ] Set all spec labels, fixed selectors, one app container and replica, Service port 80, targetPort, TCP probe, Always pull policy, rolling settings, and fixed resource budgets.
- [ ] Reject managed duplicates and unmanaged collisions with 409. Retain a created Deployment after Service failure. Report successful, failed, or unknown resource outcomes. Return 202 and Location only after both writes are accepted.

**You see.**

- [ ] POST returns 202 and Location. Two named applications serve independently through their Services. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Test strict JSON, boundary names and ports, unexpected fields, duplicate names, both unmanaged collision shapes, and all generated resource fields.
- [ ] Inject Service-create failure after Deployment success. Assert the retained Deployment and error outcomes. Test timeouts and concurrent AlreadyExists without reporting known failures as success.
- [ ] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. POST a valid app on both. Record unsupported trunk behavior if absent. Gate head 202 and eventual service access. Save `pr-02-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. POST learn-a using IMAGE_A. Run rollout status and compare Deployment and Service YAML with the spec. Save `pr-02-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. POST learn-b and port-forward both Services on 8081 and 8082. Check both return HTTP content independently. Save `pr-02-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Repeat the same name. Check 409 and unchanged resource UIDs. Save `pr-02-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. Submit malformed JSON, unknown fields, a second JSON value, invalid names, and invalid ports. Check 400 and unchanged inventory. Save `pr-02-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Compare probe values, requests 100m/64Mi, limits 500m/128Mi, rolling settings, and imagePullPolicy with kubectl YAML. Save `pr-02-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Create an isolated unmanaged Deployment collision, then POST its name. Check 409 and unchanged unmanaged UID. Save `pr-02-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Create an isolated unmanaged Service collision, then POST its name. Check no resource replacement. Save `pr-02-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Use a controlled upstream to reject Service creation. Check a retained Deployment and detailed non-202 error. Save `pr-02-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Delay sequential writes in the controlled upstream. Check one ordinary budget and unknown outcomes for uncertain writes. Save `pr-02-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. POST acceptance latency and time until service access succeeds.
- [ ] Probe. Use 10 isolated valid names per side, interleave requests, and poll rollout plus HTTP access. Time acceptance separately from convergence. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if POST exceeds 11 seconds or waits for readiness. Require service access within 180 seconds with verified images and a healthy cluster. Apply the shared 20 percent or 50 ms p95 regression rule only to equivalent implemented scenarios. These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-02-review-regression.png` and `.scratch/deployment-api/media/pr-02-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-02-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-02 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## List complete and partial applications from Kubernetes (PR-03)

**Depends on.** PR-02.

Implementation and offline checks are complete. Parent-run cluster and controlled-upstream evidence is recorded in [the runbook](runbook.md#pr-03-verification-status). The formal ten-worker and media gates below remain open.

**Files.**

- [x] Create or edit `internal/kube/apps.go`.
- [x] Create or edit `internal/kube/apps_test.go`.
- [x] Create or edit `internal/httpapi/apps.go`.
- [x] Create or edit `internal/httpapi/apps_test.go`.
- [x] Create or edit `docs/runbook.md`.

**Build.**

- [x] Add GET /apps as the sorted union of managed Deployment and Service names with presence flags.
- [x] Use both managed labels where appropriate. Exclude unmanaged objects and avoid an in-memory app registry.
- [x] Treat required list failures as upstream errors. Never return an empty successful result for an unavailable cluster.

**You see.**

- [x] Complete, Deployment-only, and Service-only apps remain discoverable after API restart. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [x] Test sorted unions, empty lists, duplicates across kinds, both partial shapes, unmanaged objects, and required read failure.
- [x] Reuse retained partial-create state from PR-02 in a create-then-list test. Assert no registry is needed after server reconstruction.
- [x] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. GET /apps with two managed apps on both. Record unsupported trunk behavior if absent. Gate head sorted union. Save `pr-03-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. List two complete apps and compare names and presence with kubectl. Save `pr-03-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. Create a lane-owned Deployment-only fixture. Check its missing Service flag. Save `pr-03-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Create a lane-owned Service-only fixture. Check its missing Deployment flag. Save `pr-03-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. Add unmanaged fixtures. Check they are excluded. Save `pr-03-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Restart the API and repeat list. Check identical resource discovery. Save `pr-03-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Use a controlled empty upstream fixture. Check `{"apps":[]}` rather than null. Save `pr-03-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Use a controlled upstream where the Deployment list fails. Check documented JSON upstream error. Save `pr-03-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Use a controlled upstream where the Service list fails. Check no misleading partial success. Save `pr-03-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Exercise create-then-list with injected Service failure. Check the retained app remains discoverable. Save `pr-03-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. GET /apps p95 latency for 20 complete or partial applications.
- [ ] Probe. Use a controlled upstream fixture of 20 apps and 100 interleaved HTTP reads on trunk and head. Also time 20 live reads with two apps. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if local fixture p95 exceeds 500 ms or an ordinary read exceeds 11 seconds. For equivalent implemented trunk scenarios, fail above max(1.2 times trunk p95, trunk p95 plus 50 ms). These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-03-review-regression.png` and `.scratch/deployment-api/media/pr-03-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-03-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-03 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Delete complete and partial applications safely (PR-04)

**Depends on.** PR-03.

**Files.**

- [ ] Create or edit `internal/kube/apps.go`.
- [ ] Create or edit `internal/kube/apps_test.go`.
- [ ] Create or edit `internal/httpapi/apps.go`.
- [ ] Create or edit `internal/httpapi/apps_test.go`.
- [ ] Create or edit `docs/runbook.md`.

**Build.**

- [ ] Implement DELETE /apps/{name}. Attempt both managed resource deletions even when one fails, subject to the shared operation deadline.
- [ ] Use ownership checks and UID delete preconditions. Never delete a colliding unmanaged resource or a replacement with a different UID.
- [ ] Return 202 for requested or ongoing deletion and 204 when both managed resources are absent. Report partial and unknown outcomes. Let Kubernetes garbage-collect owned ReplicaSets and Pods.

**You see.**

- [ ] Deletion is repeatable. Owned children disappear, the other app serves, and the namespace remains. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Test absent, partial, deleting, collision, UID replacement, either-delete failure, both attempted, and deadline outcomes.
- [ ] Assert Deployment propagation intent while keeping fake-client deletion separate from real garbage-collection proof.
- [ ] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. DELETE an owned app on both. Record missing trunk support if absent. Gate head accepted deletion and eventual cleanup. Save `pr-04-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. DELETE learn-a and wait up to 180 seconds for Deployment, Service, ReplicaSets, and Pods to disappear. Save `pr-04-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. Repeat DELETE after absence. Check 204. Save `pr-04-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Delete a Deployment-only lane fixture. Check removal. Save `pr-04-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. Delete a Service-only lane fixture. Check removal. Save `pr-04-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Delete one of two apps. Port-forward the survivor and check HTTP availability. Save `pr-04-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Check namespace deploy-api still exists after cleanup. Save `pr-04-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Use a controlled upstream that fails the first deletion. Check the second is still attempted and outcomes are accurate. Save `pr-04-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Use a controlled upstream that fails the second deletion. Check no false 202 and a retry succeeds. Save `pr-04-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Replace a UID between read and delete in a controlled fixture. Check conflict and preservation of the replacement and unmanaged objects. Save `pr-04-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. DELETE acceptance latency and time until owned children disappear.
- [ ] Probe. Use 10 isolated fixtures per side and record HTTP latency plus kubectl disappearance time, interleaved where equivalent. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if acceptance exceeds 11 seconds or deletion waits for cleanup. Require live garbage collection within 180 seconds. Apply the shared p95 regression rule only for equivalent implemented behavior. These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-04-review-regression.png` and `.scratch/deployment-api/media/pr-04-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-04-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-04 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Inspect resource presence and owned Pod diagnostics (PR-05)

Source and local offline, live inspection, restart, and controlled latency checks passed on 2026-10-22. See [the runbook](runbook.md#pr-05-local-verification). Formal independent swarm, cross-provider, and media-review gates remain open.

**Depends on.** PR-03. Deliver after PR-04 for cleanup support.

**Files.**

- [x] Create or edit `internal/kube/inspect.go`.
- [x] Create or edit `internal/kube/ownership.go`.
- [x] Create or edit `internal/kube/inspect_test.go`.
- [x] Create or edit `internal/kube/ownership_test.go`.
- [x] Create or edit `internal/httpapi/apps.go`.
- [x] Create or edit `internal/httpapi/apps_test.go`.
- [x] Create or edit `docs/runbook.md`.

**Build.**

- [x] Implement GET /apps/{name} configuration, presence, Deployment generations/counts/conditions, and Pod/container state projection.
- [x] Resolve Deployment to ReplicaSet to Pod membership by UID and labels. Share the ownership resolver with the later log endpoint.
- [x] Expose deletion timestamps, actual image, readiness, waiting/running/terminated reasons, restart counts, and last exit code/reason. Handle absent optional fields.
- [x] Return 404 only if neither managed resource exists. Return partial apps with ready=false. Keep ready=false conservatively in this PR until PR-06 implements the full predicate. Document this temporary response behavior explicitly.

**You see.**

- [x] Inspection explains resource and container state using the same owned objects visible in kubectl. Ready remains conservatively false until PR-06. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [x] Test complete and partial apps, absent resources, ownership UID mismatches, matching labels without ownership, replaced ReplicaSets, deleting Pods, optional states, and previous termination.
- [x] Test all required reads under one deadline and errors rather than misleading missing-app responses.
- [x] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Inspect a managed app on both. Record absent trunk support if needed. Gate head diagnostic projection and ownership exclusion. Save `pr-05-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. Inspect a healthy app and compare every returned field with Deployment, Service, ReplicaSet, and Pod YAML. Save `pr-05-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. Inspect a Deployment-only app. Check presence flags and ready=false. Save `pr-05-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Inspect a Service-only app. Check presence flags and no invented requested image. Save `pr-05-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. GET an absent name. Check 404. Save `pr-05-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Create an unrelated Pod with matching app labels but no correct ownership chain. Check it is excluded. Save `pr-05-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Use a controlled old-UID ReplicaSet fixture. Check its Pod is excluded. Save `pr-05-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Use a controlled restarting-container fixture. Check last termination exit code and restart count survive projection. Save `pr-05-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Use controlled waiting, running, terminated, and missing status fixtures. Check no panic or invented state. Save `pr-05-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Restart the API and inspect both complete and partial apps. Check results derive from Kubernetes. Save `pr-05-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Inspection p95 latency with 20 Pod and ReplicaSet fixture objects.
- [ ] Probe. Run 100 interleaved HTTP inspections against identical controlled upstream datasets on both revisions, plus 20 real-cluster reads. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if controlled inspection p95 exceeds 500 ms or an operation exceeds 11 seconds. For implemented equivalent scenarios, apply the shared p95 regression rule. These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-05-review-regression.png` and `.scratch/deployment-api/media/pr-05-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-05-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-05 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Compute readiness for the requested rollout (PR-06)

**Depends on.** PR-05.

**Files.**

- [ ] Create or edit `internal/kube/readiness.go`.
- [ ] Create or edit `internal/kube/readiness_test.go`.
- [ ] Create or edit `internal/kube/inspect.go`.
- [ ] Create or edit `internal/kube/inspect_test.go`.
- [ ] Create or edit `docs/runbook.md`.

**Build.**

- [ ] Implement a pure readiness predicate over the inspection snapshot. Require both non-deleting resources, observed current generation, one desired/updated/available replica, no old replicas, and a current ready Pod.
- [ ] Keep raw Deployment conditions. An available old Pod cannot make a failed requested image ready. Do not add a permanent failed application state or rollback.

**You see.**

- [ ] Ready becomes true only for the complete requested rollout and remains false during stale, partial, deleting, and failed replacement states. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Table-test current rollout success, stale generation, old healthy Pod/new unready Pod, old replicas, unavailable replica, unready current Pod, missing resources, and deletion.
- [ ] Exercise GET inspection with the predicate wired in, including conditions after ProgressDeadlineExceeded.
- [ ] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Inspect a healthy rollout on both. Trunk from PR-05 reports conservative false. Gate head true only after rollout completion. Save `pr-06-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. Wait for rollout status success, then GET inspection. Check ready=true and matching counts. Save `pr-06-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. Inspect startup while no current ready Pod exists. Check false for any captured startup snapshot. Save `pr-06-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Create a wrong-port app with IMAGE_A and containerPort 81. Check running container but ready=false. Save `pr-06-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. Inspect a partial app. Check false. Save `pr-06-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Use a controlled stale observedGeneration snapshot. Check false. Save `pr-06-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Use a controlled old healthy Pod and failed new image snapshot. Check false despite old availability. Save `pr-06-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Use a controlled old-replica snapshot with one updated available replica. Check false until old replicas are gone. Save `pr-06-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Use controlled deleting Service, Deployment, and current Pod snapshots. Check false. Save `pr-06-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Observe the wrong-port app after the controller progress deadline. Check raw ProgressDeadlineExceeded and false without automatic rollback. Save `pr-06-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. GET inspection p95 latency and time until ready=true after Kubernetes reports the matching rollout complete.
- [ ] Probe. Run 100 interleaved reads with the PR-05 dataset. Poll the live Deployment and API every second through a successful rollout. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail above max(1.2 times trunk inspection p95, trunk p95 plus 50 ms). Require ready=true within 5 seconds after a matching complete snapshot is available. Do not treat a missed transient state as proof. These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-06-review-regression.png` and `.scratch/deployment-api/media/pr-06-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-06-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-06 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Add warning events and distinguish failure causes (PR-07)

**Depends on.** PR-04 and PR-06.

**Files.**

- [ ] Create or edit `internal/kube/events.go`.
- [ ] Create or edit `internal/kube/events_test.go`.
- [ ] Create or edit `internal/kube/inspect.go`.
- [ ] Create or edit `internal/kube/inspect_test.go`.
- [ ] Create or edit `docs/runbook.md`.

**Build.**

- [ ] Attach up to 10 most recent Warning events associated by Deployment, ReplicaSet, or Pod UID. Define deterministic ordering for absent timestamps.
- [ ] On events-only read failure, preserve core inspection and return diagnosticsIncomplete=true with a readable diagnostic error. Core read failures remain HTTP errors.
- [ ] Document distinct pull, TCP readiness, and crashing-container exercises. Follow the spec crash fixture without adding environment variables, commands, or previous-log API options.

**You see.**

- [ ] Inspection shows relevant warnings and remains useful if events are unavailable. Crash diagnostics differ from pull and readiness failures. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Test UID filtering, Warning filtering, ordering, 10-event limit, absent timestamps, event-only Forbidden/timeouts, and the shared deadline.
- [ ] Assert waiting/restart/last-exit projections for the crash fixture. Do not infer a permanent application failure from a reason string.
- [ ] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Inspect a failed app on both. Gate unchanged core diagnostics plus relevant warnings on head. Save `pr-07-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. POST the intentionally missing image from Appendix D. Compare actual image-pull reasons with kubectl describe. Save `pr-07-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. POST IMAGE_A on port 81. Check running container, probe failure, and ready=false. Save `pr-07-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Execute all five steps of the spec crashing-container exercise with its recorded postgres digest. Check exit 1, restarts, previous logs, and eventual backoff after successful pull. Save `pr-07-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. Compare warning event UIDs with Deployment, ReplicaSet, and Pod UIDs. Check unrelated events are excluded. Save `pr-07-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Use a controlled fixture with more than 10 warnings. Check newest 10 and deterministic order. Save `pr-07-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Use a controlled events Forbidden response. Check core 200 and diagnosticsIncomplete=true. Save `pr-07-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Use a controlled events timeout after successful core reads. Check core response survives within the overall budget. Save `pr-07-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Observe a stalled rollout condition without requiring a particular temporary Pod phase or automatic rollback. Save `pr-07-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Delete all lane failure fixtures. Check owned children disappear within 180 seconds and namespace remains. Save `pr-07-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Inspection p95 latency including events and elapsed response time after events-only failure.
- [ ] Probe. Run 100 interleaved controlled inspections with 20 warnings and 20 live reads. Separately delay the event response until its deadline. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if successful fixture p95 exceeds max(1.2 times trunk p95, trunk p95 plus 100 ms), or event-only failure exceeds the overall 10-second budget plus 1 second tolerance. Event absence alone never fails an exercise. These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-07-review-regression.png` and `.scratch/deployment-api/media/pr-07-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-07-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-07 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Stream one selected owned Pod with cancellation (PR-08)

**Depends on.** PR-05. Deliver after PR-07 in the stack.

**Files.**

- [ ] Create or edit `internal/kube/logs.go`.
- [ ] Create or edit `internal/kube/logs_test.go`.
- [ ] Create or edit `internal/httpapi/logs.go`.
- [ ] Create or edit `internal/httpapi/logs_test.go`.
- [ ] Create or edit `internal/httpapi/server.go`.
- [ ] Create or edit `docs/runbook.md`.

**Build.**

- [ ] Implement GET /apps/{name}/logs with required pod and tailLines default 100 in range 0 through 1000. Resolve ownership before reading only the app container.
- [ ] Bound ownership lookup and connection/header establishment. Tie the stream body to request cancellation without an ordinary total timeout, rest.Config total timeout, HTTP client total timeout, or server write deadline cutting it off.
- [ ] Open upstream before writing 200 text/plain. Flush copied output, close upstream on disconnect, and record post-header failures in process logs. EOF or replacement requires explicit reconnect.

**You see.**

- [ ] Existing and new log lines arrive promptly beyond 15 seconds. Disconnect releases upstream and unrelated Pod logs never escape. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Use a controllable HTTP upstream, not typed fake logs, to test pre-header JSON errors, flushing, bounded establishment, survival beyond an injected ordinary timeout, and cancellation with synchronization.
- [ ] Test pod selection, tail bounds, ownership mismatch, not-started 409, missing Pod 404, upstream denial 503, timeout 504, EOF, and post-header failure.
- [ ] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Request selected Pod logs on both. Record unsupported trunk behavior. Gate head text/plain and new output beyond 15 seconds. Save `pr-08-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. Read POD from inspection and run `curl -N --max-time 20 "$API/apps/$APP/logs?pod=$POD&tailLines=100"`. Generate application requests immediately and after 15 seconds. Check both lines arrive. Save `pr-08-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. Compare selected Pod output with `kubectl logs "$POD" -c app --tail=100` in the fixed namespace/context. Save `pr-08-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Use tailLines 0, 100, and 1000 and invalid values. Check bounds and no unrelated output. Save `pr-08-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. Omit pod and select an absent Pod. Check 400 and 404 JSON before success headers. Save `pr-08-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Select a real unrelated Pod. Check ownership rejection and no leaked logs. Save `pr-08-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. Use a controlled not-started container and initial upstream errors. Check useful JSON 409, 503, or 504 before streaming. Save `pr-08-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Disconnect curl and use the synchronized controlled-upstream driver to confirm request cancellation and body close. Save `pr-08-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Use a controlled stalled establishment. Check its bound without applying the deadline to an established body. Save `pr-08-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. End the upstream stream or replace a lane-owned Pod. Check EOF or closure, logged post-header failure when applicable, and explicit reconnect without automatic switching. Save `pr-08-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. New-line delivery latency, stream lifetime, and upstream cancellation latency.
- [ ] Probe. Use a controlled upstream that timestamps 20 lines and disconnect events. Run trunk first, then alternate revisions. On the live cluster generate access logs at 0 and 15 seconds during a 20-second curl stream. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if controlled line delivery or cancellation takes over 1 second, or a healthy live stream closes before 15 seconds. Require establishment within 10 seconds plus 1 second tolerance. Use ratios only if trunk supports equivalent streaming. These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-08-review-regression.png` and `.scratch/deployment-api/media/pr-08-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-08-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-08 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Update images and verify the complete lifecycle (PR-09)

**Depends on.** PR-04 and PR-06. Deliver after PR-08 in the stack.

**Files.**

- [ ] Create or edit `internal/kube/apps.go`.
- [ ] Create or edit `internal/kube/apps_test.go`.
- [ ] Create or edit `internal/httpapi/apps.go`.
- [ ] Create or edit `internal/httpapi/apps_test.go`.
- [ ] Create or edit `docs/runbook.md`.

**Build.**

- [ ] Implement PATCH /apps/{name}/image with strict image-only JSON. Update only the intended Deployment app container image under the ordinary operation deadline.
- [ ] Return 409 for partial or deleting apps and write conflicts. Return 202 with name and status URL without rollout waiting. Identical image reference is a no-op with no generation increment.
- [ ] Preserve Service identity, selectors, port, replica count, probe, resource budgets, and pull policy. Use different verified digests for a real rollout.
- [ ] Run the complete spec acceptance sequence and record each criterion with evidence. Add no rollback, scaling, storage, private registry, or platform framework.

**You see.**

- [ ] A different image rolls out with a stable Service. A bad update is unready even when the old app serves, and a valid update recovers. Save HTTP transcripts and matching Kubernetes evidence.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Test strict image-only decode, empty image, missing/partial/deleting apps, identical no-op, write conflicts, image-only mutation, and old-healthy/new-unready inspection.
- [ ] Run accumulated offline tests and race detection. Map all 10 specification acceptance criteria to named evidence, marking any blocked exercise unverified.
- [ ] Run `go test ./...`, `go vet ./...`, and `go build ./...`. Save commands, exit codes, and output. All default tests run offline.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked. Ten lanes on the configured `swarm workers` role at the PR head, per the boot recipe.

- [ ] Lane 1. Regression lane against trunk. Run create, inspect, service access, and image PATCH on both. Record missing update support on trunk. Gate unchanged existing lifecycle and successful head rollout. Save `pr-09-lane-01.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 2. Record Service UID/address and Deployment YAML. PATCH IMAGE_B and observe Pod replacement plus rollout success within 180 seconds. Save `pr-09-lane-02.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 3. Compare replacement Pod budgets, probes, selectors, and pull policy. Check Service UID/address and desired replica count unchanged. Save `pr-09-lane-03.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 4. Repeat identical IMAGE_B PATCH. Check unchanged Deployment generation and no image mutation request in the controlled fixture. Save `pr-09-lane-04.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 5. PATCH the intentionally missing image. Check new pull failure, old availability where observed, and API ready=false. Save `pr-09-lane-05.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 6. Port-forward through the surviving old Pod during failed rollout. Check access if old capacity remains, recording actual capacity or forwarding failures. Save `pr-09-lane-06.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 7. PATCH back to IMAGE_A. Check convergence and ready=true without requiring old Pod or ReplicaSet reuse. Save `pr-09-lane-07.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 8. Attempt image PATCH against partial, deleting, and conflicting controlled fixtures. Check 409 with no unrelated mutations. Save `pr-09-lane-08.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 9. Create two apps, restart the API, list and inspect both, delete one, and port-forward the other. Check isolation and surviving logs. Save `pr-09-lane-09.png`. Pass when the stated checks hold and the matching raw transcript records the result.
- [ ] Lane 10. Delete all lane apps and stop forwards. Check no lane Deployments, Services, ReplicaSets, or Pods within 180 seconds and namespace remains. Record the full acceptance evidence map. Save `pr-09-lane-10.png`. Pass when the stated checks hold and the matching raw transcript records the result.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. PATCH acceptance latency, time until requested rollout ready, and HTTP availability samples during update.
- [ ] Probe. Record trunk acceptance for supported operations first. Alternate 10 isolated updates per implemented revision, polling current generation/ready and service HTTP every second. Record rollout time separately from acceptance. Save per-sample durations, dataset, image digests, and cluster conditions.
- [ ] Baseline. Record trunk measurements first at its exact SHA. Record an unsupported scenario explicitly when trunk lacks it. Both sides still measure the common health request and endpoint response latency, including unsupported responses. Never compare unsupported latency with successful operation latency as a speedup.
- [ ] Rule. Fail if PATCH exceeds 11 seconds, waits for rollout, or a verified healthy-image rollout fails to converge within 180 seconds on a healthy cluster. Require identical-reference no-op and stable Service identity. Report observed availability without promising zero downtime. Apply ratios only to equivalent implemented scenarios. These are proposed acceptance budgets, not measured results. Record external cluster or registry blockers separately and rerun before checking this box.

**Review gate.** The operator reviews before merge.

- [ ] Copy lane 1 and lane 2 screenshots into `.scratch/deployment-api/media/pr-09-review-regression.png` and `.scratch/deployment-api/media/pr-09-review-result.png`.
- [ ] Record a 30 to 60 second video of the changed API interaction in the terminal. Save `.scratch/deployment-api/media/pr-09-review.mp4`.
- [ ] Post the screenshots and the video in chat. Stop at stack-ready. The operator reviews and lands in order.

**Merge.**

- [ ] Record the root clean verdict at the exact head SHA, passing CI, and completed Bugbot triage.
- [ ] Record verdict SHA, landing SHA, patch base, and patch ID. Use Shipping rules for post-parent landing moves and preserve verification only when the patch ID rule permits it.
- [ ] Append PR-09 to the frozen stack. Same-repository children target their parent branch. Fork children target trunk with local parent ancestry. The operator lands the current bottom PR. No owner merges.

## Close the program

- [ ] Every required box has linked evidence at the verified SHA. An unexecuted or externally blocked check stays unchecked.
- [ ] Record all 10 spec acceptance criteria in `docs/runbook.md` with unit versus cluster evidence distinguished. Explain the resource hierarchy and failure diagnoses using the captured objects.
- [ ] Deliver the frozen PR-01 through PR-09 stack with per-head verdicts, outstanding operator gates, and receipts. End implementation scope here.

## Appendix A. Prototype evidence

No prototypes ran during planning. There is no application to drive yet. The existing specification fixes the resource model and public behavior, so this plan does not claim an experimentally selected architecture.

Unproven work includes registry availability, actual image digests, local capacity, live convergence times, and indefinite stream transport behavior. PR-01 checks the environment. PR-02 records image and cluster evidence. PR-08 uses a controlled upstream and a live stream to prove transport lifetimes. These checks must pass during execution.

The specification mentions a missing subject for review point 1. Neither document identifies the requested change. This plan covers the written specification only and does not invent an amendment. Record any later clarification before changing scope.

## Appendix B. Alternatives rejected

A single CRUD PR would combine resource construction, partial writes, discovery, and garbage collection. Separate PRs isolate those failure boundaries.

Request validation stays with POST rather than becoming a PR with a temporary success stub. Inspection splits at the ownership snapshot and readiness predicate. PR-05 deliberately reports conservative false until PR-06, so it never reports a false successful rollout.

An in-memory app registry loses partial apps and restart discovery. Kubernetes resource union is the specified source of truth. Automatic repair and rollback would hide the partial state this exercise is meant to expose.

No ticket publication or application scaffolding belongs to this documentation change. The prior draft's external ticket workflow is not required to execute this plan.

## Appendix C. Risks

No Git metadata, module, application, CI, or repository instructions were found in the working directory. Resolve repository initialization, module identity, canonical forge repository, trunk, and remotes before PR-01 publishing. Do not invent them during planning.

PR-05 temporarily reports ready=false for all apps. The operator must understand that checkpoint. PR-06 completes readiness before failure exercises or update acceptance depend on it.

PR-02 and PR-04 span multiple Kubernetes writes. Timeout outcomes can remain unknown, and retries must begin with inspection. Collision and UID replacement tests guard against deleting unrelated resources.

PR-07 depends on temporary events and registry behavior. Missing events do not imply success. Start the crash observation bound after successful pull and record actual reasons. Do not treat network failure as a crashing process.

PR-08 must separate connection establishment and body lifetime. A total client or server timeout can silently truncate logs. Controlled transport tests and the 20-second live exercise are both required.

Ten simultaneous workload lanes may exceed Docker Desktop capacity with the fixed requests and rolling surge. Use disjoint names and serialize cluster lanes when necessary. Never mistake resource contention for an application timing result.

There is no separate installed HTTP driver skill. On Codex, shell execution of curl and kubectl supplies the CLI driver behavior. Save raw evidence and terminal screenshots. Do not claim an observation based only on a screenshot of a test summary.

The planning explorer on the default judgment role `claude:opus@xhigh` dropped out before reading the documents. The CLI authentication preflight returned loggedIn=false and status unauthenticated. No substitute model ran. The parent inspected both documents and checked this plan directly. Independent explorer review remains unverified. The local receipt is `/tmp/deploy-api-plan/explore.json`.

## Appendix D. Links and reading list

Read [the specification](spec.md) before every PR. Add `docs/runbook.md` in PR-01 and update only the steps that each PR implements. All paths in Files blocks are proposed locations, not claims about existing code.

At execution start, read the installed plugin's `skills/how/SKILL.md` for every PR and `skills/interrogate/SKILL.md` for the partial-write, ownership, readiness, streaming, and image-update boundaries in PR-02 and PR-04 through PR-09. Read `skills/show-me-your-work/SKILL.md` and maintain `.scratch/deployment-api/decisions.tsv`. Read the sequence, prove-it-works, benchmark-checklist, deslop, and no-comments skills when their steps apply.

Use these setup commands before workload exercises. The namespace is manually prepared and never created or deleted by the Go API.

```sh
go version
kubectl --context=docker-desktop version -o json
kubectl --context=docker-desktop get nodes -o custom-columns=NAME:.metadata.name,OS:.status.nodeInfo.operatingSystem,ARCH:.status.nodeInfo.architecture
kubectl --context=docker-desktop get namespace deploy-api
```

If the namespace is absent, manually create it with `kubectl --context=docker-desktop create namespace deploy-api` during authorized setup. Do not change the user's current context.

Inspect `docker.io/library/nginx:stable-alpine` and `docker.io/library/nginx:mainline-alpine` with `docker buildx imagetools inspect`. Confirm linux/arm64 for each. Record different index digests and set IMAGE_A and IMAGE_B to the corresponding immutable `docker.io/library/nginx@sha256` references. Never invent digests or silently substitute latest. For the crash fixture, follow [the spec exercise](spec.md#reproducible-crashing-container-exercise) in full.

Create partial fixtures only from a lane-owned managed app or a manifest copied from its captured resource intent. Preserve owner UIDs in fixtures where membership matters. A test driver must never impersonate the user's kubeconfig or change the real cluster endpoint to inject failures.

Use the following command forms with lane-owned names and recorded image references. Set API to `http://127.0.0.1:8080`, APP to the isolated fixture name, and POD to an owned Pod from inspection. Variables exist only in the verification shell.

```sh
curl --fail-with-body -i -H 'Content-Type: application/json' -d "{\"name\":\"$APP\",\"image\":\"$IMAGE_A\",\"containerPort\":80}" "$API/apps"
curl --fail-with-body "$API/apps"
curl --fail-with-body "$API/apps/$APP"
kubectl --context=docker-desktop -n deploy-api get deployment "$APP" -o yaml
kubectl --context=docker-desktop -n deploy-api get service "$APP" -o yaml
kubectl --context=docker-desktop -n deploy-api get replicasets,pods -l "app.kubernetes.io/name=$APP" -o yaml
kubectl --context=docker-desktop -n deploy-api rollout status "deployment/$APP" --timeout=180s
kubectl --context=docker-desktop -n deploy-api port-forward "service/$APP" 8081:80
```

Keep the forward running in a separate terminal. Generate access log lines with `curl --fail http://127.0.0.1:8081/`. Restart forwarding if its selected Pod exits. Use these commands after their endpoint PRs exist.

```sh
curl -N --max-time 20 "$API/apps/$APP/logs?pod=$POD&tailLines=100"
kubectl --context=docker-desktop -n deploy-api logs "$POD" -c app --tail=100
curl --fail-with-body -i -X PATCH -H 'Content-Type: application/json' -d "{\"image\":\"$IMAGE_B\"}" "$API/apps/$APP/image"
curl --fail-with-body -i -X DELETE "$API/apps/$APP"
kubectl --context=docker-desktop -n deploy-api get deployments,services,replicasets,pods -l "app.kubernetes.io/name=$APP"
kubectl --context=docker-desktop get namespace deploy-api
```

Use `docker.io/library/nginx:deploy-api-intentionally-missing-20261021` for the negative pull/update exercise. Confirm the actual reason from Kubernetes rather than assuming registry reachability.

Map spec acceptance 1, 2, and resource budgets from 10 to PR-02. Map 6 and 7 to PR-03 through PR-05. Map 8 to PR-04. Map readiness in 3 to PR-06, failure diagnosis in 3 and 9 to PR-07, logs in 5 to PR-08, and updates in 4 to PR-09. PR-09 replays all criteria and verifies replacement budgets from 10. Default offline tests accompany every PR.
