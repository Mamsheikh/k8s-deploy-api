const POLL_INTERVAL = 5000;
const LOG_LIMIT = 200000;
const state = {
  apps: { items: [], loadedAt: null, error: null, loading: true },
  selectedName: null,
  selectionGeneration: 0,
  snapshots: new Map(),
  query: "",
  tab: "overview",
  dialog: null,
  deleting: new Set(),
  poll: { generation: 0, controller: null, timer: null, running: false, rerun: false, stopped: false },
  logs: { podName: "", tailLines: 100, status: "idle", controller: null, reader: null, generation: 0, text: "", error: null, trimmed: false, refs: null },
};
const refs = Object.fromEntries([
  "new-app", "notice", "total-count", "ready-count", "attention-count", "summary-note", "nav-count",
  "summary-context", "inventory-count", "inventory-error", "refresh-state", "refresh", "search", "app-list", "detail",
  "app-dialog", "dialog-form", "dialog-title", "dialog-description", "dialog-fields", "dialog-error",
  "dialog-submit", "dialog-cancel", "dialog-close", "dialog-symbol",
].map((id) => [id, document.getElementById(id)]));
const logOutput = element("pre", "empty");
logOutput.tabIndex = 0;
logOutput.setAttribute("aria-label", "Pod log output");
let detailKey = "";

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function actionButton(label, className, action, name) {
  const node = element("button", className, label);
  node.type = "button";
  node.dataset.action = action;
  if (name) node.dataset.name = name;
  return node;
}

function replaceContent(container, ...children) {
  const active = document.activeElement;
  const focusKey = container.contains(active) ? active.dataset.focusKey : null;
  container.replaceChildren(...children);
  if (focusKey) {
    const target = Array.from(container.querySelectorAll("[data-focus-key]")).find((node) => node.dataset.focusKey === focusKey);
    target?.focus({ preventScroll: true });
  }
}

function timeLabel(value) {
  if (!value) return "Not observed yet";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function formatError(error) {
  return error?.message || "Could not reach the API. Check that the local server is running and retry.";
}

function errorContent(error, lead) {
  const box = element("div");
  if (lead) box.append(element("strong", "", lead));
  box.append(element("p", "", formatError(error)));
  const details = error?.details;
  if (details?.reason || error?.code) box.append(element("p", "mono", [error.code, details?.reason].filter(Boolean).join(" · ")));
  if (details?.resources) {
    box.append(element("p", "mono", Object.entries(details.resources).map(([name, result]) => `${name}: ${result}`).join(" · ")));
  }
  for (const [name, failure] of Object.entries(details?.failures || {})) {
    box.append(element("p", "", `${name}: ${failure.reason || failure.code || "error"} · ${failure.message || ""}`));
  }
  return box;
}

async function responseError(response) {
  let info;
  const text = await response.text();
  try { info = JSON.parse(text).error; } catch {}
  const error = new Error(info?.message || `The API returned HTTP ${response.status}. ${text.slice(0, 200).trim()}`);
  Object.assign(error, info, { status: response.status });
  return error;
}

async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { Accept: "application/json", ...(options.body ? { "Content-Type": "application/json" } : {}), ...options.headers } });
  if (!response.ok) throw await responseError(response);
  return response.status === 204 ? null : response.json();
}

function snapshot(name) {
  if (!state.snapshots.has(name)) state.snapshots.set(name, { data: null, loadedAt: null, error: null, loading: false });
  return state.snapshots.get(name);
}

function appStatus(app, entry = state.snapshots.get(app.name)) {
  if (state.deleting.has(app.name)) return { label: "Deleting", tone: "deleting" };
  if (entry?.error || state.apps.error) return { label: entry.data ? "Stale" : "Unknown", tone: "stale" };
  const data = entry?.data;
  if (!data) return !app.resources.deployment || !app.resources.service ? { label: "Incomplete", tone: "attention" } : { label: "Not inspected", tone: "unknown" };
  if (data.deployment?.deletionTimestamp || data.service?.deletionTimestamp) return { label: "Deleting", tone: "deleting" };
  if (!data.resources.deployment || !data.resources.service) return { label: "Incomplete", tone: "attention" };
  if (data.ready) return { label: "Ready", tone: "ready" };
  const waiting = data.pods?.map((pod) => pod.container?.state?.waiting?.reason).find((reason) => reason && !["ContainerCreating", "PodInitializing"].includes(reason));
  const terminated = data.pods?.some((pod) => pod.container?.state?.terminated?.exitCode > 0);
  const stalled = data.deployment?.conditions?.some((condition) => condition.reason === "ProgressDeadlineExceeded" || condition.type === "ReplicaFailure" && condition.status === "True");
  if (waiting || terminated || stalled) return { label: "Needs attention", tone: "attention" };
  return { label: "Deploying", tone: "pending" };
}

function statusBadge(status, pill = false) {
  return element("span", `status ${status.tone}${pill ? " pill" : ""}`, status.label);
}

function showNotice(text, warning = false) {
  refs.notice.hidden = false;
  refs.notice.classList.toggle("warning", warning);
  const dismiss = actionButton("×", "", "dismiss-notice");
  dismiss.setAttribute("aria-label", "Dismiss notification");
  refs.notice.replaceChildren(element("span", "", text), dismiss);
}

function renderSummary() {
  const apps = state.apps.items;
  const statuses = apps.map((app) => appStatus(app));
  const unknown = statuses.filter((status) => ["unknown", "stale"].includes(status.tone)).length;
  const deploying = statuses.filter((status) => status.tone === "pending").length;
  const deleting = statuses.filter((status) => status.tone === "deleting").length;
  const loaded = state.apps.loadedAt !== null;
  refs["total-count"].textContent = loaded ? apps.length : "·";
  refs["nav-count"].textContent = loaded ? apps.length : "·";
  refs["inventory-count"].textContent = loaded ? apps.length : "·";
  refs["ready-count"].textContent = loaded ? statuses.filter((status) => status.tone === "ready").length : "·";
  refs["attention-count"].textContent = loaded ? statuses.filter((status) => status.tone === "attention").length : "·";
  refs["summary-note"].textContent = unknown ? `${unknown} unknown or stale` : deploying ? `${deploying} deploying` : deleting ? `${deleting} deleting` : "diagnostics to review";
  const pendingNotes = [unknown ? `${unknown} unknown or stale` : "", deploying ? `${deploying} deploying` : "", deleting ? `${deleting} deleting` : ""].filter(Boolean);
  refs["summary-context"].hidden = !loaded || !pendingNotes.length;
  refs["summary-context"].textContent = `${pendingNotes.join(" · ")}. Counts use current inspection results.`;
  const pollLabel = state.apps.error ? "Refresh failed · showing last observation" : state.poll.running ? "Refreshing observations…" : state.apps.loadedAt ? `Observed ${timeLabel(state.apps.loadedAt)} · auto refresh 5s` : "Connecting to the API…";
  refs["refresh-state"].textContent = pollLabel;
  refs["refresh-state"].classList.toggle("error", Boolean(state.apps.error));
  refs.refresh.disabled = state.poll.running;
  refs["inventory-error"].hidden = !state.apps.error;
  if (state.apps.error) refs["inventory-error"].replaceChildren(errorContent(state.apps.error, state.apps.loadedAt ? "Inventory is stale. Your last successful observation is still visible." : "Applications could not be loaded."));
}

function renderInventory() {
  const filtered = state.apps.items.filter((app) => app.name.includes(state.query.toLowerCase()));
  if (!filtered.length) {
    const empty = element("div", "empty-list");
    if (state.query) empty.append(element("strong", "", "No matching applications"), element("p", "", "Try another application name."));
    else if (state.apps.loading && !state.apps.loadedAt) empty.append(element("strong", "", "Reading the namespace"), element("p", "", "Your applications will appear here."));
    else if (state.apps.error && !state.apps.loadedAt) empty.append(element("strong", "", "Connection unavailable"), element("p", "", "Check the API and refresh to try again."), actionButton("Try again", "button secondary small", "refresh"));
    else empty.append(element("strong", "", "A clean slate"), element("p", "", "Deploy your first container image to get started."), actionButton("＋ New application", "button secondary small", "create"));
    replaceContent(refs["app-list"], empty);
    return;
  }
  const cards = filtered.map((app) => {
    const entry = state.snapshots.get(app.name);
    const card = actionButton("", `app-card${app.name === state.selectedName ? " selected" : ""}`, "select-app", app.name);
    card.dataset.focusKey = `app-${app.name}`;
    card.setAttribute("aria-pressed", String(app.name === state.selectedName));
    const content = element("div", "app-card-content");
    const title = element("div", "app-card-title");
    const name = element("span", "app-name mono", app.name);
    name.title = app.name;
    title.append(name);
    const meta = element("div", "app-card-meta");
    meta.append(statusBadge(appStatus(app)), element("span", "", !app.resources.deployment || !app.resources.service ? "Partial resources" : app.namespace));
    const image = element("span", "app-card-image mono", entry?.data?.image || (entry?.loading ? "Inspecting configuration…" : "Deployment + Service"));
    if (entry?.data?.image) image.title = entry.data.image;
    content.append(title, meta, image);
    card.append(element("span", "app-icon", "◇"), content, element("span", "app-card-arrow", "›"));
    return card;
  });
  replaceContent(refs["app-list"], ...cards);
}

function render() {
  renderSummary();
  renderInventory();
  renderDetail();
}

function datum(label, value, wide = false, mono = false) {
  const item = element("div", `datum${wide ? " wide" : ""}`);
  item.append(element("dt", "", label), element("dd", mono ? "mono" : "", value ?? "Not available"));
  return item;
}

function renderOverview(data) {
  const view = element("div");
  view.append(element("h3", "section-label", "Configuration"));
  const config = element("dl", "overview-grid");
  const image = datum("Container image", data.image || "No managed Deployment", true, true);
  image.lastElementChild.classList.add("image-value");
  const resources = datum("Managed resources", "", true);
  const chips = element("div", "resource-row");
  chips.append(element("span", `resource-chip${data.resources.deployment ? "" : " missing"}`, `Deployment ${data.resources.deployment ? "present" : "missing"}`), element("span", `resource-chip${data.resources.service ? "" : " missing"}`, `Service ${data.resources.service ? "present" : "missing"}`));
  resources.lastElementChild.append(chips);
  config.append(image, datum("Container port", data.containerPort === undefined ? null : `${data.containerPort} / TCP`, false, true), datum("Namespace", data.namespace, false, true), resources);
  view.append(config);
  if (!data.resources.deployment || !data.resources.service) {
    const warning = element("div", "inline-alert");
    warning.append(element("strong", "", "This application is incomplete."), element("p", "", "A managed resource is missing. Inspect the remaining resources below. Delete and recreate the application to recover."));
    view.append(element("div", "divider"), warning);
  }
  if (data.deployment) {
    const deployment = data.deployment;
    view.append(element("div", "divider"), element("h3", "section-label", "Rollout"));
    const rollout = element("div", "rollout");
    for (const [label, key] of [["Desired", "desiredReplicas"], ["Updated", "updatedReplicas"], ["Ready", "readyReplicas"], ["Available", "availableReplicas"]]) {
      const item = element("div", "rollout-item");
      item.append(element("strong", "mono", deployment[key]), element("span", "", label));
      rollout.append(item);
    }
    view.append(rollout);
    const generation = element("div", "generation-line");
    generation.append(element("span", "", `Generation ${deployment.generation} · observed ${deployment.observedGeneration}`), element("span", "", `${deployment.replicas} total replica${deployment.replicas === 1 ? "" : "s"}`));
    view.append(generation, element("div", "divider"));
    const heading = element("div", "conditions-header");
    heading.append(element("h3", "section-label", "Deployment conditions"), element("span", "conditions-note", "Reported by Kubernetes"));
    view.append(heading);
    if (!deployment.conditions?.length) view.append(element("p", "subtle-note", "The controller has not reported any conditions yet."));
    for (const condition of deployment.conditions || []) {
      const item = element("div", "condition");
      const top = element("div", "condition-top");
      top.append(element("span", "condition-type", condition.type), element("span", `condition-value${condition.status === "True" ? "" : " false"}`, condition.status), element("span", "condition-reason mono", condition.reason || "No reason reported"));
      item.append(top, element("p", "", condition.message || "No message reported."));
      if (condition.lastTransitionTime) {
        const time = element("time", "", `Last transition ${new Date(condition.lastTransitionTime).toLocaleString()}`);
        time.dateTime = condition.lastTransitionTime;
        item.append(time);
      }
      view.append(item);
    }
  }
  if (data.deployment?.deletionTimestamp || data.service?.deletionTimestamp) view.append(element("p", "subtle-note", "Deletion is in progress. Kubernetes removes dependent Pods asynchronously."));
  return view;
}

function renderDetail() {
  const name = state.selectedName;
  const entry = name ? snapshot(name) : null;
  const data = entry?.data;
  const observationError = entry?.error || state.apps.error;
  const app = state.apps.items.find((item) => item.name === name);
  const key = JSON.stringify([name, state.tab, data, entry?.error?.message, entry?.error?.status, entry?.loading && !data, state.logs.podName, state.deleting.has(name), state.apps.loadedAt !== null, Boolean(state.apps.error)]);
  refs.detail.setAttribute("aria-busy", String(Boolean(entry?.loading && !data || !state.apps.loadedAt && state.apps.loading)));
  if (detailKey === key) {
    const observed = refs.detail.querySelector("[data-observed]");
    if (observed) observed.textContent = `${entry?.error ? "Last good observation" : "Last observed"} ${timeLabel(entry?.loadedAt)}`;
    return;
  }
  detailKey = key;
  const logFocused = document.activeElement === logOutput;
  const logScrollTop = logOutput.scrollTop;
  state.logs.refs = null;
  if (!name) {
    const empty = element("div", "empty-detail");
    const hasLoaded = state.apps.loadedAt !== null;
    empty.append(element("span", "empty-symbol", "◇"), element("h2", "", state.apps.error ? "Your cluster is out of view" : hasLoaded ? "Your next idea starts here" : "Connecting the dots"), element("p", "", state.apps.error ? "The API could not load applications. Check the connection, then refresh to continue." : hasLoaded ? "Give a public container image a name and a port. We’ll help you follow its journey into Kubernetes." : "Loading applications from your local Kubernetes namespace."));
    empty.append(actionButton(state.apps.error ? "Try again" : "＋ Deploy an application", "button primary", state.apps.error ? "refresh" : "create"));
    replaceContent(refs.detail, empty);
    return;
  }
  const header = element("div", "detail-header");
  const identity = element("div", "detail-identity");
  const titles = element("div");
  const title = element("h2", "detail-name", name);
  const subtitle = element("div", "detail-subtitle");
  subtitle.append(element("span", "mono", data?.namespace || app?.namespace || "deploy-api"), statusBadge(appStatus(app || { name, resources: data?.resources || {} }, entry), true));
  titles.append(title, subtitle);
  identity.append(element("span", "detail-icon", "◇"), titles);
  const actions = element("div", "detail-actions");
  const update = actionButton("↗ Update image", "button secondary small", "update");
  update.dataset.focusKey = "update";
  update.disabled = !data || !data.resources.deployment || !data.resources.service || Boolean(data.deployment?.deletionTimestamp || data.service?.deletionTimestamp || state.deleting.has(name));
  if (update.disabled) update.title = "Image updates require a complete application that is not deleting.";
  const remove = actionButton("Delete", "button ghost-danger small", "delete");
  remove.dataset.focusKey = "delete";
  actions.append(update, remove);
  header.append(identity, actions);
  const children = [header];
  if (observationError) {
    const alert = element("div", "inline-alert detail-alert");
    alert.append(errorContent(observationError, data ? `Inspection is stale. Showing the last good observation from ${timeLabel(entry.loadedAt)}.` : "Application inspection is unavailable."));
    if (observationError.status === 404) alert.append(element("p", "", "Neither managed resource was found. The inventory will reconcile on the next refresh."));
    children.push(alert);
  }
  if (data) {
    const tabs = element("div", "detail-tabs");
    tabs.setAttribute("role", "tablist");
    tabs.setAttribute("aria-label", "Application diagnostics");
    for (const [id, label] of [["overview", "Overview"], ["pods", "Pods & logs"], ["events", "Events"]]) {
      const tab = actionButton(label, "tab", "tab", id);
      tab.dataset.focusKey = `tab-${id}`;
      tab.id = `tab-${id}`;
      tab.setAttribute("role", "tab");
      tab.setAttribute("aria-selected", String(state.tab === id));
      tab.setAttribute("aria-controls", "detail-panel");
      tab.tabIndex = state.tab === id ? 0 : -1;
      if (id === "pods") tab.append(element("span", "tab-count", data.pods?.length || 0));
      if (id === "events") tab.append(element("span", "tab-count", data.warningEvents?.length || 0));
      tabs.append(tab);
    }
    const body = element("div", "detail-body");
    body.id = "detail-panel";
    body.setAttribute("role", "tabpanel");
    body.setAttribute("aria-labelledby", `tab-${state.tab}`);
    body.tabIndex = 0;
    body.dataset.focusKey = "detail-panel";
    body.append(state.tab === "pods" ? renderPods(data) : state.tab === "events" ? renderEvents(data) : renderOverview(data));
    const observation = element("div", "detail-observation");
    const observed = element("span", "", `${entry.error ? "Last good observation" : "Last observed"} ${timeLabel(entry.loadedAt)}`);
    observed.dataset.observed = "true";
    observation.append(observed, element("span", "", "Kubernetes is the source of truth"));
    children.push(tabs, body, observation);
  } else {
    const body = element("div", "detail-body");
    body.append(element("h3", "section-label", entry?.loading ? "Reading application state" : "No inspection available"));
    if (entry?.loading) for (let index = 0; index < 3; index++) body.append(element("div", "loading-line"));
    else body.append(element("p", "subtle-note", "Refresh to request current configuration and diagnostics."), actionButton("Refresh inspection", "button secondary small", "refresh"));
    children.push(body);
  }
  replaceContent(refs.detail, ...children);
  if (refs.detail.contains(logOutput)) {
    logOutput.scrollTop = logScrollTop;
    if (logFocused) logOutput.focus({ preventScroll: true });
  }
  updateLogUI();
}

function containerState(pod) {
  if (pod.deletionTimestamp) return "Terminating";
  const value = pod.container?.state;
  if (value?.waiting) return value.waiting.reason || "Waiting";
  if (value?.terminated) return value.terminated.reason || `Terminated (${value.terminated.exitCode})`;
  if (value?.running) return "Running";
  return pod.phase || "Pending";
}

function renderPods(data) {
  const view = element("div");
  view.append(element("h3", "section-label", "Owned Pods"));
  const pods = data.pods || [];
  if (pods.length) {
    const wrap = element("div", "pod-table-wrap");
    const table = element("table", "pod-table");
    const head = element("thead");
    const headings = element("tr");
    for (const label of ["Pod", "Container state", "Ready", "Restarts"]) {
      const th = element("th", "", label);
      th.scope = "col";
      headings.append(th);
    }
    head.append(headings);
    const body = element("tbody");
    for (const pod of pods) {
      const selected = pod.name === state.logs.podName;
      const row = element("tr", selected ? "selected" : "");
      const nameCell = element("td");
      const name = actionButton(pod.name, `pod-name${selected ? " selected" : ""}`, "select-pod", pod.name);
      name.dataset.focusKey = `pod-${pod.name}`;
      name.title = pod.name;
      name.setAttribute("aria-pressed", String(selected));
      nameCell.append(name);
      row.append(nameCell, element("td", "mono", containerState(pod)), element("td", "", pod.ready ? "Yes" : "No"), element("td", "mono", pod.container?.restartCount ?? "—"));
      body.append(row);
    }
    table.append(head, body);
    wrap.append(table);
    view.append(wrap);
  } else view.append(element("p", "subtle-note", data.resources.deployment ? "No owned Pods have been observed yet. The controller may still be creating them." : "This application has no managed Deployment, so there are no owned Pods to inspect."));
  const selectedPod = pods.find((pod) => pod.name === state.logs.podName);
  if (selectedPod) {
    const diagnostics = element("div", "pod-diagnostics");
    const grid = element("dl", "overview-grid");
    grid.append(datum("Actual image", selectedPod.container?.image, true, true));
    if (selectedPod.container?.imageID) grid.append(datum("Image ID", selectedPod.container.imageID, true, true));
    grid.append(datum("Pod phase", selectedPod.phase), datum("Container state", containerState(selectedPod)));
    const current = selectedPod.container?.state;
    const last = selectedPod.container?.lastTerminationState?.terminated;
    if (current?.waiting?.message) grid.append(datum("Waiting message", current.waiting.message, true));
    if (current?.terminated) grid.append(datum("Termination", `Exit ${current.terminated.exitCode} · ${current.terminated.reason || "No reason reported"}${current.terminated.message ? ` · ${current.terminated.message}` : ""}`, true));
    if (last) grid.append(datum("Previous termination", `Exit ${last.exitCode} · ${last.reason || "No reason reported"}${last.message ? ` · ${last.message}` : ""}`, true));
    if (selectedPod.deletionTimestamp) grid.append(datum("Deletion requested", new Date(selectedPod.deletionTimestamp).toLocaleString(), true));
    diagnostics.append(grid);
    view.append(diagnostics);
  }
  const heading = element("div", "log-header");
  heading.append(element("h3", "", "Container logs"), element("span", "", "One Pod. One container instance."));
  const controls = element("div", "log-controls");
  const podLabel = element("label");
  podLabel.append(element("span", "control-label", "Pod"));
  const podSelect = element("select");
  podSelect.id = "log-pod";
  podSelect.dataset.focusKey = "log-pod";
  const placeholder = element("option", "", "Choose a Pod");
  placeholder.value = "";
  podSelect.append(placeholder);
  for (const pod of pods) {
    const option = element("option", "", pod.name);
    option.value = pod.name;
    podSelect.append(option);
  }
  podSelect.value = state.logs.podName;
  podSelect.addEventListener("change", () => selectPod(podSelect.value));
  podLabel.append(podSelect);
  const tailLabel = element("label");
  tailLabel.append(element("span", "control-label", "Tail lines"));
  const tail = element("select");
  tail.dataset.focusKey = "log-tail";
  for (const number of [0, 100, 300, 1000]) {
    const option = element("option", "", number);
    option.value = number;
    tail.append(option);
  }
  tail.value = state.logs.tailLines;
  tail.addEventListener("change", () => { state.logs.tailLines = Number(tail.value); });
  tailLabel.append(tail);
  const connect = actionButton("Connect", "button primary small", "toggle-logs");
  connect.dataset.focusKey = "connect-logs";
  controls.append(podLabel, tailLabel, connect);
  const error = element("div", "inline-alert");
  error.setAttribute("role", "status");
  const console = element("div", "console");
  const top = element("div", "console-top");
  const streamStatus = statusBadge({ label: "Disconnected", tone: "unknown" });
  top.append(element("span", "console-title mono", "app · stdout / stderr"), streamStatus);
  console.append(top, logOutput);
  const note = element("div", "console-note");
  const limitNote = element("span", "", "Latest 200k characters kept in this browser");
  note.append(element("span", "", "Streams end on restart or replacement. Reconnect explicitly."), limitNote);
  view.append(heading, controls, error, console, note);
  state.logs.refs = { connect, tail, error, streamStatus, limitNote };
  return view;
}

function renderEvents(data) {
  const view = element("div");
  view.append(element("h3", "section-label", "Recent warning events"));
  if (data.diagnosticsIncomplete) {
    const alert = element("div", "inline-alert");
    alert.append(errorContent(data.diagnosticError, "Event diagnostics are incomplete."));
    view.append(alert);
  }
  if (!data.warningEvents?.length) {
    const empty = element("div", "events-empty");
    empty.append(element("span", "", data.diagnosticsIncomplete ? "!" : "◇"), element("h3", "", data.diagnosticsIncomplete ? "Events could not be read" : "No recent warnings observed"), element("p", "", data.diagnosticsIncomplete ? "Core application diagnostics remain available. The next refresh will retry event lookup." : "Kubernetes events are temporary. An empty list does not establish application health."));
    view.append(empty);
  }
  for (const event of data.warningEvents || []) {
    const item = element("article", "event-card");
    const top = element("div", "event-top");
    top.append(element("strong", "event-reason", event.reason || "Warning"));
    if (event.timestamp) {
      const time = element("time", "", new Date(event.timestamp).toLocaleString());
      time.dateTime = event.timestamp;
      top.append(time);
    }
    const object = event.involvedObject;
    item.append(top, element("p", "", event.message || "No message reported."), element("span", "event-object mono", object ? `${object.kind || "Resource"} / ${object.name || object.uid || "Unknown"}` : "Associated resource not reported"));
    view.append(item);
  }
  view.append(element("p", "subtle-note", "Up to 10 recent warnings associated with this Deployment, its ReplicaSets and owned Pods."));
  return view;
}

function updateLogUI() {
  const logs = state.logs;
  const active = ["connecting", "streaming"].includes(logs.status);
  const ui = logs.refs;
  if (ui) {
    ui.connect.textContent = active ? "Disconnect" : ["ended", "disconnected", "error"].includes(logs.status) ? "Reconnect" : "Connect";
    ui.connect.disabled = !logs.podName;
    ui.connect.className = `button ${active ? "secondary" : "primary"} small`;
    ui.tail.disabled = active;
    ui.error.hidden = !logs.error;
    if (logs.error) ui.error.replaceChildren(errorContent(logs.error));
    const labels = { idle: "Disconnected", connecting: "Connecting…", streaming: "Streaming", ended: "Stream ended", disconnected: "Disconnected", error: "Connection error" };
    ui.streamStatus.textContent = labels[logs.status];
    ui.streamStatus.className = `status ${logs.status === "streaming" ? "ready" : "unknown"}`;
    ui.limitNote.textContent = logs.trimmed ? "Older output trimmed · latest 200k characters shown" : "Latest 200k characters kept in this browser";
  }
  const atBottom = logOutput.scrollHeight - logOutput.scrollTop - logOutput.clientHeight < 45;
  const placeholders = { idle: "Select a Pod, then connect to follow its current container.", connecting: "Opening the log stream…", streaming: "Connected. Waiting for log output…", ended: "The stream ended. Reconnect to follow the current container instance.", disconnected: "Disconnected. Reconnect when you are ready.", error: "The log stream could not be opened. Review the error above and reconnect." };
  logOutput.textContent = logs.text || placeholders[logs.status];
  logOutput.classList.toggle("empty", !logs.text);
  if (atBottom || !logs.text) logOutput.scrollTop = logOutput.scrollHeight;
}

function stopLogs(status = "disconnected", clear = false) {
  const logs = state.logs;
  logs.generation++;
  logs.controller?.abort();
  if (logs.reader) void logs.reader.cancel().catch(() => {});
  logs.controller = null;
  logs.reader = null;
  logs.status = status;
  logs.error = null;
  if (clear) { logs.text = ""; logs.trimmed = false; }
  updateLogUI();
}

function selectPod(name) {
  if (state.logs.podName === name) return;
  stopLogs("idle", true);
  state.logs.podName = name;
  renderDetail();
}

async function connectLogs() {
  const logs = state.logs;
  const name = state.selectedName;
  const podName = logs.podName;
  if (!name || !podName) return;
  stopLogs("connecting", true);
  const generation = logs.generation;
  const selection = state.selectionGeneration;
  const controller = new AbortController();
  logs.controller = controller;
  const ownsStream = () => generation === logs.generation && selection === state.selectionGeneration && name === state.selectedName && podName === logs.podName && !controller.signal.aborted;
  updateLogUI();
  let reader;
  try {
    const query = new URLSearchParams({ pod: podName, tailLines: String(logs.tailLines) });
    const response = await fetch(`/apps/${encodeURIComponent(name)}/logs?${query}`, { signal: controller.signal, headers: { Accept: "text/plain" } });
    if (!response.ok) throw await responseError(response);
    if (!ownsStream()) { await response.body?.cancel(); return; }
    if (!response.body) throw new Error("This browser did not provide a streaming response body.");
    reader = response.body.getReader();
    logs.reader = reader;
    logs.status = "streaming";
    updateLogUI();
    const decoder = new TextDecoder();
    while (ownsStream()) {
      const { done, value } = await reader.read();
      if (!ownsStream()) return;
      const chunk = decoder.decode(value, { stream: !done });
      logs.text += chunk;
      if (logs.text.length > LOG_LIMIT) {
        logs.text = logs.text.slice(-LOG_LIMIT);
        logs.trimmed = true;
      }
      if (done) logs.status = "ended";
      updateLogUI();
      if (done) break;
    }
  } catch (error) {
    if (ownsStream()) {
      logs.status = "error";
      logs.error = error;
      updateLogUI();
    }
  } finally {
    reader?.releaseLock();
    if (ownsStream()) { logs.reader = null; logs.controller = null; }
  }
}

function selectApp(name) {
  if (state.selectedName === name) return;
  stopLogs("idle", true);
  state.logs.podName = "";
  state.selectedName = name;
  state.selectionGeneration++;
  state.tab = "overview";
  render();
  refresh();
}

function refresh() {
  clearTimeout(state.poll.timer);
  if (state.poll.stopped || state.dialog?.busy) return;
  if (state.poll.running) {
    state.poll.rerun = true;
    state.poll.controller?.abort();
    return;
  }
  void refreshCycle();
}

async function refreshCycle() {
  const poll = state.poll;
  poll.running = true;
  poll.rerun = false;
  const generation = ++poll.generation;
  const controller = new AbortController();
  poll.controller = controller;
  const current = () => generation === poll.generation && !controller.signal.aborted && !poll.stopped;
  state.apps.loading = true;
  renderSummary();
  try {
    let result;
    try { result = await api("/apps", { signal: controller.signal }); }
    catch (error) {
      if (current()) { state.apps.error = error; render(); }
      return;
    }
    if (!current()) return;
    state.apps.items = result.apps;
    state.apps.loadedAt = Date.now();
    state.apps.error = null;
    const names = new Set(result.apps.map((app) => app.name));
    for (const name of state.deleting) {
      if (!names.has(name)) { state.deleting.delete(name); showNotice(`${name} has been removed from the namespace.`); }
    }
    for (const name of state.snapshots.keys()) if (!names.has(name)) state.snapshots.delete(name);
    if (!names.has(state.selectedName)) {
      stopLogs("idle", true);
      state.logs.podName = "";
      state.selectedName = result.apps[0]?.name || null;
      state.selectionGeneration++;
      state.tab = "overview";
    }
    const queue = [...result.apps].sort((a, b) => Number(b.name === state.selectedName) - Number(a.name === state.selectedName));
    for (const app of queue) snapshot(app.name).loading = true;
    render();
    let index = 0;
    async function inspectNext() {
      while (index < queue.length && current()) {
        const app = queue[index++];
        const entry = snapshot(app.name);
        try {
          const data = await api(`/apps/${encodeURIComponent(app.name)}`, { signal: controller.signal });
          if (!current()) return;
          entry.data = data;
          entry.loadedAt = Date.now();
          entry.error = null;
          if (app.name === state.selectedName) {
            const pods = data.pods || [];
            if (state.logs.podName && !pods.some((pod) => pod.name === state.logs.podName)) {
              stopLogs("disconnected");
              state.logs.podName = "";
              state.logs.error = new Error("The selected Pod is no longer present. Choose a Pod and reconnect.");
            } else if (!state.logs.podName && state.logs.status === "idle" && pods.length) state.logs.podName = pods[0].name;
          }
        } catch (error) {
          if (!current()) return;
          entry.error = error;
        } finally {
          entry.loading = false;
        }
        if (current()) render();
      }
    }
    await Promise.all([inspectNext(), inspectNext()]);
  } finally {
    if (generation === poll.generation) {
      for (const entry of state.snapshots.values()) entry.loading = false;
      state.apps.loading = false;
      poll.running = false;
      poll.controller = null;
      render();
      if (!poll.stopped && !state.dialog?.busy) {
        if (poll.rerun) { poll.rerun = false; void refreshCycle(); }
        else poll.timer = setTimeout(refresh, POLL_INTERVAL);
      }
    }
  }
}

function formField(labelText, name, options = {}) {
  const label = element("label", "form-field");
  label.append(element("span", "", labelText));
  const input = element("input", options.mono ? "mono" : "");
  input.name = name;
  input.id = `field-${name}`;
  input.required = true;
  input.autocomplete = "off";
  for (const [key, value] of Object.entries(options)) {
    if (!["hint", "mono"].includes(key)) input[key] = value;
  }
  label.append(input);
  if (options.hint) {
    const hint = element("small", "", options.hint);
    hint.id = `hint-${name}`;
    input.setAttribute("aria-describedby", hint.id);
    label.append(hint);
  }
  return label;
}

function openDialog(kind) {
  const name = state.selectedName;
  if (kind !== "create" && !name) return;
  const data = name ? snapshot(name).data : null;
  state.dialog = { kind, name, busy: false, error: null, controller: null };
  refs["dialog-form"].reset();
  refs["dialog-fields"].replaceChildren();
  refs["dialog-error"].hidden = true;
  refs["dialog-symbol"].className = `dialog-symbol${kind === "delete" ? " danger" : ""}`;
  refs["dialog-symbol"].textContent = kind === "delete" ? "−" : kind === "update" ? "↗" : "＋";
  if (kind === "create") {
    refs["dialog-title"].textContent = "New application";
    refs["dialog-description"].textContent = "Start with a public container image. Kubernetes will create a Deployment and a Service in deploy-api.";
    refs["dialog-fields"].append(
      formField("Application name", "name", { placeholder: "my-application", pattern: "[a-z]([a-z0-9\\-]{0,61}[a-z0-9])?", maxLength: 63, mono: true, hint: "1–63 lowercase letters, numbers or hyphens. Start with a letter and end with a letter or number." }),
      formField("Container image", "image", { placeholder: "registry/image:tag", mono: true, hint: "Use a public image with a tag or digest and a compatible architecture." }),
      formField("Container port", "containerPort", { type: "number", min: "1", max: "65535", step: "1", placeholder: "8080", hint: "The TCP port your application listens on. The Service uses port 80." }),
    );
  } else if (kind === "update") {
    refs["dialog-title"].textContent = "Update container image";
    refs["dialog-description"].textContent = `Start a rolling update for ${name}. Its port, Service and selectors stay the same.`;
    refs["dialog-fields"].append(formField("Container image", "image", { value: data?.image || "", placeholder: "registry/image:new-tag", mono: true, hint: "A different image reference starts a rollout. The same reference is a no-op. Kubernetes does not automatically roll back." }));
  } else {
    refs["dialog-title"].textContent = "Delete this application?";
    refs["dialog-description"].textContent = "This requests deletion of the managed Deployment and Service. Kubernetes will also remove their owned Pods and ReplicaSets.";
    refs["dialog-fields"].append(element("div", "delete-name mono", name), element("p", "subtle-note", "The application remains visible until its managed resources are gone. This action cannot be undone."));
  }
  updateDialog();
  refs["app-dialog"].showModal();
  if (kind === "delete") refs["dialog-cancel"].focus();
  else refs["dialog-fields"].querySelector("input")?.focus();
}

function updateDialog() {
  const dialog = state.dialog;
  if (!dialog) return;
  const label = { create: "Create application", update: "Update image", delete: "Delete application" }[dialog.kind];
  refs["dialog-submit"].textContent = dialog.busy ? "Submitting…" : label;
  refs["dialog-submit"].className = `button ${dialog.kind === "delete" ? "danger" : "primary"}`;
  refs["dialog-submit"].disabled = dialog.busy;
  refs["dialog-cancel"].disabled = dialog.busy;
  refs["dialog-close"].disabled = dialog.busy;
  refs["dialog-form"].setAttribute("aria-busy", String(dialog.busy));
  for (const input of refs["dialog-fields"].querySelectorAll("input")) input.disabled = dialog.busy;
  refs["dialog-error"].hidden = !dialog.error;
  if (dialog.error) refs["dialog-error"].replaceChildren(errorContent(dialog.error, "The request did not complete successfully."));
}

function closeDialog() {
  if (state.dialog?.busy) return;
  state.dialog = null;
  refs["app-dialog"].close();
}

async function submitDialog(event) {
  event.preventDefault();
  const dialog = state.dialog;
  if (!dialog || dialog.busy) return;
  const values = new FormData(refs["dialog-form"]);
  const name = dialog.kind === "create" ? String(values.get("name")).trim() : dialog.name;
  const image = String(values.get("image") || "").trim();
  const body = dialog.kind === "create" ? { name, image, containerPort: Number(values.get("containerPort")) } : { image };
  dialog.busy = true;
  dialog.error = null;
  dialog.controller = new AbortController();
  clearTimeout(state.poll.timer);
  state.poll.rerun = false;
  state.poll.controller?.abort();
  updateDialog();
  try {
    const path = dialog.kind === "create" ? "/apps" : `/apps/${encodeURIComponent(name)}${dialog.kind === "update" ? "/image" : ""}`;
    await api(path, { method: { create: "POST", update: "PATCH", delete: "DELETE" }[dialog.kind], ...(dialog.kind === "delete" ? {} : { body: JSON.stringify(body) }), signal: dialog.controller.signal });
    dialog.busy = false;
    closeDialog();
    if (dialog.kind === "delete") {
      state.deleting.add(name);
      stopLogs("disconnected");
      showNotice(`Deletion requested for ${name}. Waiting for its managed resources to disappear.`);
    } else {
      if (state.selectedName !== name) {
        stopLogs("idle", true);
        state.logs.podName = "";
        state.selectedName = name;
        state.selectionGeneration++;
      }
      state.tab = "overview";
      showNotice(dialog.kind === "create" ? `${name} was accepted. Kubernetes is creating its resources; readiness will follow.` : `Image update accepted for ${name}. Inspecting the requested rollout.`);
    }
    render();
  } catch (error) {
    dialog.busy = false;
    dialog.error = error;
    updateDialog();
    if (Object.values(error.details?.resources || {}).some((outcome) => ["created", "delete_requested", "deleting", "unknown"].includes(outcome))) showNotice("A resource write may have partially completed. Inspect the observed resources before retrying.", true);
  } finally {
    dialog.controller = null;
    refresh();
  }
}

function handleAction(event) {
  const target = event.target.closest("[data-action]");
  if (!target || target.disabled) return;
  switch (target.dataset.action) {
    case "create": openDialog("create"); break;
    case "update": openDialog("update"); break;
    case "delete": openDialog("delete"); break;
    case "refresh": refresh(); break;
    case "dismiss-notice": refs.notice.hidden = true; break;
    case "select-app": selectApp(target.dataset.name); break;
    case "select-pod": selectPod(target.dataset.name); break;
    case "tab": state.tab = target.dataset.name; renderDetail(); break;
    case "toggle-logs":
      if (["connecting", "streaming"].includes(state.logs.status)) stopLogs();
      else void connectLogs();
      break;
  }
}

refs["new-app"].addEventListener("click", () => openDialog("create"));
refs.refresh.addEventListener("click", refresh);
refs.search.addEventListener("input", () => { state.query = refs.search.value.trim(); renderInventory(); });
refs["app-list"].addEventListener("click", handleAction);
refs.detail.addEventListener("click", handleAction);
refs.notice.addEventListener("click", handleAction);
refs["dialog-form"].addEventListener("submit", submitDialog);
refs["dialog-cancel"].addEventListener("click", closeDialog);
refs["dialog-close"].addEventListener("click", closeDialog);
refs["app-dialog"].addEventListener("cancel", (event) => {
  if (state.dialog?.busy) event.preventDefault();
  else state.dialog = null;
});
refs.detail.addEventListener("keydown", (event) => {
  if (event.target.getAttribute("role") !== "tab" || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const tabs = ["overview", "pods", "events"];
  const index = tabs.indexOf(state.tab);
  state.tab = event.key === "Home" ? tabs[0] : event.key === "End" ? tabs[2] : tabs[(index + (event.key === "ArrowRight" ? 1 : 2)) % tabs.length];
  renderDetail();
  document.getElementById(`tab-${state.tab}`)?.focus();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "/" && !state.dialog && !["INPUT", "TEXTAREA", "SELECT"].includes(event.target.tagName) && !event.metaKey && !event.ctrlKey && !event.altKey) {
    event.preventDefault();
    refs.search.focus();
  }
});
window.addEventListener("pagehide", () => {
  state.poll.stopped = true;
  clearTimeout(state.poll.timer);
  state.poll.controller?.abort();
  stopLogs();
});
window.addEventListener("pageshow", (event) => {
  if (event.persisted) { state.poll.stopped = false; refresh(); }
});
render();
refresh();
