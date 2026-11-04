<script lang="ts">
  import { onMount, tick, untrack } from "svelte";
  import { MediaQuery } from "svelte/reactivity";
  import {
    mdiViewDashboard,
    mdiFileDocumentOutline,
    mdiAccountMultipleOutline,
    mdiCalculator,
    mdiFlash,
    mdiPrinter3dNozzle,
    mdiPrinter3d,
    mdiBookOpenVariant,
    mdiCogOutline,
    mdiChevronLeft,
    mdiChevronRight,
    mdiInboxOutline,
    mdiFileDocumentMultipleOutline,
    mdiLogout,
    mdiFormatLineSpacing,
    mdiOpenInNew,
    mdiStorefrontOutline,
    mdiMenu,
  } from "@mdi/js";
  import { authRequired, loadConfig } from "./lib/api";
  import { setErrorCurrencySymbol } from "./lib/serverErrors";
  import { accessWatch, installAccessWatch, resetAccessRevoked } from "./lib/accessWatch.svelte";
  import { DEFAULT_FETCH_TIMEOUT_MS } from "./lib/fetchTimeouts";
  import { fluiddUrl } from "./lib/moonraker";
  import { extractConfigUser, hasTabAccess, ungrantedTabs } from "./lib/permissions";
  import { hasShopActivity } from "./lib/landing";
  import { listQuotes } from "./lib/quotesApi";
  import { loadRequests } from "./lib/requestsApi";
  import { listInvoices } from "./lib/invoicesApi";
  import { signOutAction } from "./lib/accessSignIn";
  import { density, toggleDensity } from "./lib/density.svelte";
  import { applyConfig, appConfig, effectiveConfig, printJob, invoicePrintJob, quoteActions, pendingOpen, quoteSession, startNewQuote, quoteIsDirty } from "./lib/state.svelte";
  import {
    demoLocal,
    initDemoLocal,
    setTheme as setDemoTheme,
    setCurrency as setDemoCurrency,
    setUserMode as setDemoUserMode,
    CURRENCY_PRESET_OPTIONS,
    type ThemeChoice,
    type CurrencyPresetId,
    type UserModeChoice,
  } from "./lib/demoLocal.svelte";
  import { preloadPrintLogo } from "./lib/printLogo.svelte";
  import { settingsDirty } from "./lib/settingsDirty.svelte";
  import { pageSave, runPageSave } from "./lib/pageShortcuts.svelte";
  import { unsavedForm } from "./lib/unsavedForm.svelte";
  import { unreadRequests, refreshUnread } from "./lib/requestsBadge.svelte";
  import { brandTab, fallbackIfDisabled, filterNavSections, firstNavTab, resolveHashTab } from "./lib/navFilter";
  import { loadPublicConfig } from "./public/api";
  import { splitHash, quoteRouteBase, quoteIdFromHash } from "./lib/hashRoute";
  import { focusAfterUpdate } from "./lib/a11y.svelte";
  import { pageTitle as pageTitleState } from "./lib/pageTitle.svelte";
  import LiveAnnouncer from "./components/LiveAnnouncer.svelte";
  import Icon from "./components/Icon.svelte";
  import logoUrl from "./assets/alpaca-logo.svg";
  import QuoteTab from "./components/QuoteTab.svelte";
  import QuotesTab from "./components/QuotesTab.svelte";
  import ElectricityTab from "./components/ElectricityTab.svelte";
  import FilamentTab from "./components/FilamentTab.svelte";
  import ClientsTab from "./components/ClientsTab.svelte";
  import InvoicesTab from "./components/InvoicesTab.svelte";
  import ItemsTab from "./components/ItemsTab.svelte";
  import RequestsTab from "./components/RequestsTab.svelte";
  import PrintersTab from "./components/PrintersTab.svelte";
  import DashboardTab from "./components/DashboardTab.svelte";
  import PrintView from "./components/PrintView.svelte";
  import InvoicePrintView from "./components/InvoicePrintView.svelte";
  import GettingStarted from "./components/GettingStarted.svelte";
  import SettingsTab from "./components/SettingsTab.svelte";
  import LoginScreen from "./components/LoginScreen.svelte";

  // Read-only alias: appConfig.current (state.svelte.ts) is the source of truth. Declared early because
  // tabFromHash's initial call reads disabledTabs during synchronous setup, before config loads (empty then;
  // the $effect below corrects the initial tab).
  const config = $derived(effectiveConfig(appConfig.current));
  // UI decluttering only, never access control.
  const disabledTabs = $derived(new Set(config?.ui.disabledTabs));

  // /api/config's "user" extra (server/users.go's meResponse); null until the first response lands, which
  // every permission check treats as "everything granted".
  const configUser = $derived(extractConfigUser(config));

  // Installed once, synchronously during setup, before any child issues its first request. The closures read
  // `config` and `configUser` live, since neither is known until the first /api/config response lands. This
  // keeps demo mode's expected 403s from tripping it (see accessWatch.svelte.ts), and lets a 403 from a
  // cross-feature fetch (e.g. QuotesTab reading /api/invoices for an identity granted only "quotes") read as
  // expected rather than a revocation, without a permission check at every call site.
  installAccessWatch(() => config?.demoMode === true, () => configUser);

  let configFailed = $state(false);
  // True while GET /api/config's latest response was a 401 (auth enabled, no valid session): LoginScreen
  // replaces the app shell until login succeeds. See authRequired in lib/api.ts (a module flag read right
  // after each loadConfig()).
  let loginRequired = $state(false);

  // Also render LoginScreen, with a notice, when any other mid-session /api/* request 401s
  // (accessWatch.sessionExpired). Read directly in the template condition rather than mirrored into local
  // $state: it is already reactive and a tab's in-flight fetch can flip it at any time, so the shell switches
  // on the next flush ("never render tab content states from 401 responses").
  const SESSION_EXPIRED_NOTICE = "You were signed out: your session reached its 30-day limit, you signed out on another device, or your password changed.";

  // Single source of truth for every tab: route hash, nav label and icon, keyed by Tab id. Adding a tab means
  // one entry here; HASH_TABS and tabFromStorage derive from it and NAV_SECTIONS references its ids, so a
  // typo is a type error.
  const TABS = {
    quote: { hash: "#/quote", label: "New quote", icon: mdiCalculator },
    quotes: { hash: "#/quotes", label: "Quotes", icon: mdiFileDocumentMultipleOutline },
    electricity: { hash: "#/electricity", label: "Electricity", icon: mdiFlash },
    filament: { hash: "#/filament", label: "Filament", icon: mdiPrinter3dNozzle },
    printers: { hash: "#/printers", label: "Printers", icon: mdiPrinter3d },
    clients: { hash: "#/clients", label: "Clients", icon: mdiAccountMultipleOutline },
    invoices: { hash: "#/invoices", label: "Invoices", icon: mdiFileDocumentOutline },
    items: { hash: "#/items", label: "Shop items", icon: mdiStorefrontOutline },
    requests: { hash: "#/requests", label: "Requests", icon: mdiInboxOutline },
    dashboard: { hash: "#/dashboard", label: "Dashboard", icon: mdiViewDashboard },
    guide: { hash: "#/guide", label: "Guide", icon: mdiBookOpenVariant },
    settings: { hash: "#/settings", label: "Settings", icon: mdiCogOutline },
  } as const;
  type Tab = keyof typeof TABS;

  // Reverse lookup (route hash to Tab) derived from TABS; resolveHashTab wants a plain Partial<Record<string,
  // Tab>> since it must also reject a hash naming no tab.
  const HASH_TABS: Partial<Record<string, Tab>> = Object.fromEntries(
    (Object.keys(TABS) as Tab[]).map((id) => [TABS[id].hash, id]),
  );
  const TAB_STORAGE_KEY = "printpaca-tab";

  function tabFromHash(hash: string): Tab | null {
    // CardHelp's "More in the guide" links append a second fragment ("#/guide#margin-vs-markup"); splitHash
    // strips it before the exact-match lookup, or every anchored link would fail to navigate.
    const { base } = splitHash(hash);
    // A hash naming a disabled tab resolves to null like an unrecognized one; every caller already falls back
    // from null (Quote on initial load, no-op on hashchange).
    return resolveHashTab(quoteRouteBase(base), HASH_TABS, disabledTabs);
  }

  // Hands a deep-linked invoice/request/client id to the tab about to mount via pendingOpen (state.svelte.ts)
  // instead of each tab parsing location.hash. Called only when a fragment could matter (initial load, a
  // hashchange switching ONTO those tabs), never while already on the tab, so an in-app action that set the
  // box just before its own bare-hash activate() isn't clobbered. Once mounted, the tab owns its id changes
  // via its own hashchange listener.
  function routePendingOpen(t: Tab, hash: string) {
    // The quote editor's address is #/quote/<id>: the id travels the same way as the other tabs' fragments.
    if (t === "quote") {
      const id = quoteIdFromHash(hash);
      if (id) pendingOpen.current = { tab: "quote", id };
      return;
    }
    if (t !== "invoices" && t !== "requests" && t !== "clients") return;
    const { fragment: id } = splitHash(hash);
    if (!id) return;
    pendingOpen.current = { tab: t, id };
  }

  function tabFromStorage(): Tab | null {
    try {
      const v = localStorage.getItem(TAB_STORAGE_KEY);
      return v !== null && v in TABS ? (v as Tab) : null;
    } catch {
      return null;
    }
  }
  function persistTab(t: Tab) {
    try {
      localStorage.setItem(TAB_STORAGE_KEY, t);
    } catch {
      /* Safari private mode / quota: losing "last visited" isn't fatal. */
    }
  }

  // Priority: a valid location.hash (deep link, reload), then the last-visited tab from localStorage, then
  // "quote". `initialTab` is a plain local so persisting it doesn't trip Svelte's "state referenced locally"
  // warning.
  const hashTab = tabFromHash(location.hash);
  const storedTab = tabFromStorage();
  const initialTab: Tab = hashTab ?? storedTab ?? "quote";
  // No hash and no remembered tab: the default landing, which becomes the Dashboard once the shop has any
  // quote, request, or invoice (see the landing effect below).
  let landedByDefault = hashTab === null && storedTab === null;
  let tab = $state<Tab>(initialTab);
  // True when `tab` came from an explicit, valid hash (deep link, typed URL, bookmark) rather than
  // localStorage or the "quote" default. The permission fallback below redirects an ungranted default landing
  // tab but leaves an explicit navigation to an ungranted tab on the "No access" card. Kept in sync at every
  // site that assigns `tab`.
  let tabExplicit = $state(hashTab !== null);
  persistTab(initialTab); // every activation writes the key, including this initial one
  // Route a pending id only when the hash produced initialTab (hashTab === initialTab); a second fragment on
  // an absent or invalid hash belongs to nothing.
  if (hashTab === initialTab) routePendingOpen(initialTab, location.hash);

  // On desktop the app-main element is the scroll container (so the sidebar stays viewport-pinned). It never
  // unmounts between tabs, so reset its scrollTop on every tab change. A no-op on mobile, where the window
  // scrolls.
  let mainEl: HTMLElement | undefined = $state();
  function resetMainScroll() {
    if (mainEl) mainEl.scrollTop = 0;
  }

  // At >=900px mainEl is the sole scroll container: .sidebar and .demo-banner sit in overflow:hidden cells,
  // so a wheel over them would do nothing. Forward it to mainEl, except where something between the target
  // and this element can still scroll itself (.side-nav's overflow-y: auto, which must keep working).
  // Registered via onwheel in the markup: Svelte forces passive only on touchstart/touchmove, so the compiled
  // wheel listener is non-passive and preventDefault() below works.
  function forwardWheelToMain(event: WheelEvent) {
    if (!mainEl) return;
    // At <900px .sidebar is a slide-in drawer that can dispatch wheel events here (closed it is off-screen,
    // not display:none), but the window scrolls, not mainEl. mainEl.scrollHeight > clientHeight is true only
    // once the >=900px rule makes main an overflow-y: auto grid item, so this is a cheaper check than
    // getComputedStyle, and there is nothing to forward to when main has no internal scroll range.
    if (mainEl.scrollHeight <= mainEl.clientHeight) return;

    const deltaY = normalizeWheelDeltaY(event);
    const listener = event.currentTarget; // the sidebar <aside> or the demo-banner <p>

    for (
      let el = event.target instanceof HTMLElement ? event.target : null;
      el && el !== listener;
      el = el.parentElement
    ) {
      if (el.scrollHeight <= el.clientHeight) continue; // not a scroller at all
      const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight;
      const atTop = el.scrollTop <= 0;
      if ((deltaY > 0 && !atBottom) || (deltaY < 0 && !atTop)) return; // let it scroll itself instead
    }

    mainEl.scrollTop += deltaY;
    event.preventDefault();
  }

  // Firefox defaults wheel events to DOM_DELTA_LINE, where deltaY is a line count, not pixels; 16px per line
  // matches browsers' own figure. DOM_DELTA_PAGE (rare) scales by mainEl's height.
  function normalizeWheelDeltaY(event: WheelEvent): number {
    if (event.deltaMode === 1) return event.deltaY * 16;
    if (event.deltaMode === 2) return event.deltaY * (mainEl?.clientHeight ?? 0);
    return event.deltaY;
  }

  // href="#main" is kept for semantics (a real fragment link), but following it would set location.hash =
  // "#main" and clobber the tab router. Handle activation here: move focus, leave the URL.
  async function focusMain() {
    await tick();
    mainEl?.focus({ preventScroll: true });
  }

  function skipToMain(e: MouseEvent) {
    e.preventDefault();
    mainEl?.focus({ preventScroll: true });
  }

  // Data-loss guard: SettingsEditor is embedded by several tabs and unmounts on tab switch, which would
  // silently discard an in-progress edit. Native confirm() is a deliberate exception to the app's two-click
  // delete pattern: navigating away loses data instantly, with no in-page place for a second click.
  function activate(t: Tab) {
    // A disabled tab has no nav button, but a few flows call activate() directly (e.g. RequestsTab's
    // onOpenInvoice) with a destination that may have gone dark; stay put instead of mounting a tab the
    // sidebar would never show.
    if (disabledTabs.has(t)) return;
    if (t !== tab && (settingsDirty.current || unsavedForm.current) && !confirm("You have unsaved changes. Leave without saving?")) {
      return;
    }
    tab = t;
    persistTab(t);
    resetMainScroll();
    if (location.hash !== TABS[t].hash) location.hash = TABS[t].hash; // pushes a history entry, enabling back/forward
    // Selecting a destination from the drawer closes it (closeDrawer is a no-op when shut). <main> is inert
    // while the drawer is open, so focus it only after the flush; outside the drawer it takes focus now.
    // Either way keyboard and screen-reader focus lands on the new page, like the skip link.
    closeDrawer(false);
    void focusMain();
  }

  // Syncs `tab` on hashchange (back/forward, manual URL edits); added and removed like the afterprint
  // listener below.
  function syncTabFromHash() {
    const t = tabFromHash(location.hash);
    if (t) {
      const changingTab = t !== tab;
      // Same data-loss guard as activate(): a hashchange (back/forward, edited URL, another tab's link) can
      // switch tabs just like a sidebar click.
      if (changingTab && (settingsDirty.current || unsavedForm.current) && !confirm("You have unsaved changes. Leave without saving?")) {
        // Restore the hash via replaceState, not `location.hash =`, which would push a spurious history entry
        // and re-fire this handler.
        history.replaceState(null, "", TABS[tab].hash);
        return;
      }
      tab = t;
      tabExplicit = true; // see its declaration above: this hashchange named a real tab explicitly
      persistTab(t);
      resetMainScroll();
      // Only when this hashchange switches onto invoices/requests/clients (about to mount fresh); see
      // routePendingOpen for why it stays off while already on the tab.
      if (changingTab) {
        routePendingOpen(t, location.hash);
        // Back/forward and in-app hash links skip activate(), so move focus here too (WCAG 2.4.3).
        void focusMain();
      }
    } else {
      // Bogus hash, or one naming a tab disabled by now (config is loaded by the time a hashchange fires):
      // rewrite the address bar to the tab actually showing. replaceState, so no spurious history entry and
      // no re-fire.
      history.replaceState(null, "", TABS[tab].hash);
    }
  }

  // SettingsEditor.save() writes appConfig directly and never calls refreshConfig, so re-enabling Requests
  // from Settings updates disabledTabs but leaves the unread badge stale. Track requests' previous membership
  // in disabledTabs and fetch only on the falling edge (disabled to enabled). Initialized false to match
  // disabledTabs' empty initial value, so config-not-loaded to loaded is never mistaken for a real
  // transition.
  let requestsWasDisabled = false;
  // Server validation messages quote money in cents; they are shown in the configured currency.
  $effect(() => {
    setErrorCurrencySymbol(appConfig.current?.currency.symbol ?? "$");
  });

  $effect(() => {
    const isDisabled = disabledTabs.has("requests");
    // The falling edge is also reachable for an identity never granted "requests" (disabledTabs is
    // independent of per-user grants); gate the fetch like refreshConfig's boot-time fetch, or the false
    // "your access changed" banner reappears.
    if (requestsWasDisabled && !isDisabled && hasTabAccess("requests", configUser)) refreshUnread();
    requestsWasDisabled = isDisabled;
  });

  // Keeps printLogo.readyUrl warm from config resolution, well before either print view mounts for
  // window.print() (see printLogo.svelte.ts). Re-runs when the effective logoUrl changes.
  $effect(() => {
    preloadPrintLogo(config?.quote.logoUrl);
  });

  const SIDEBAR_STORAGE_KEY = "printpaca-sidebar";
  function loadSidebarCollapsed(): boolean {
    try {
      return localStorage.getItem(SIDEBAR_STORAGE_KEY) === "collapsed";
    } catch {
      return false;
    }
  }
  let sidebarCollapsed = $state(loadSidebarCollapsed());
  function toggleSidebar() {
    sidebarCollapsed = !sidebarCollapsed;
    try {
      localStorage.setItem(SIDEBAR_STORAGE_KEY, sidebarCollapsed ? "collapsed" : "expanded");
    } catch {
      /* Safari private mode / quota: losing the collapse preference isn't fatal. */
    }
  }

  // Below 900px .sidebar is the same markup as a slide-in drawer (one nav, not a second copy). isDesktop
  // tracks which the viewport is, for what CSS can't express: navCollapsed (icon-only is a desktop-only
  // preference; the drawer always shows labels) and auto-closing the drawer if a resize crosses 900px while
  // open, so drawer state never leaks into the >=900px layout.
  const desktopQuery = new MediaQuery("min-width: 900px", true);
  const isDesktop = $derived(desktopQuery.current);
  const navCollapsed = $derived(sidebarCollapsed && isDesktop);

  let drawerOpen = $state(false);
  let asideEl: HTMLElement | undefined = $state();
  let hamburgerEl: HTMLButtonElement | undefined = $state();
  // Focus moves only after Svelte flushes the DOM: while closed the <aside> is inert (focus() is ignored), and
  // once open the <header> and <main> go inert, so focusing the hamburger or <main> early would be dropped
  // (WCAG 2.4.3).
  function openDrawer() {
    drawerOpen = true;
    void focusAfterUpdate(() => asideEl?.querySelector<HTMLElement>("button, a[href]"));
  }
  function closeDrawer(returnFocus = true) {
    if (!drawerOpen) return;
    drawerOpen = false;
    if (returnFocus) void focusAfterUpdate(() => hamburgerEl);
  }
  function toggleDrawer() {
    if (drawerOpen) closeDrawer();
    else openDrawer();
  }

  $effect(() => {
    if (isDesktop) untrack(() => closeDrawer(false));
  });

  // Escape only while the drawer is open, via an $effect whose cleanup removes the listener, instead of one
  // permanent window listener re-checking drawerOpen on every keystroke.
  $effect(() => {
    if (!drawerOpen) return;
    function onKeydown(e: KeyboardEvent) {
      if (e.key === "Escape") closeDrawer();
    }
    window.addEventListener("keydown", onKeydown);
    return () => window.removeEventListener("keydown", onKeydown);
  });

  // Background scroll lock while the drawer covers the screen: a class toggle on <body>
  // (:global(body.drawer-open)), so there is no prior inline overflow value to restore.
  $effect(() => {
    document.body.classList.toggle("drawer-open", drawerOpen);
  });

  onMount(() => {
    // Hydrate the URL to match the resolved initial tab (e.g. from localStorage with no hash) via
    // replaceState, so there is no spurious back-button stop. Gated on tabFromHash, not an exact
    // TABS[tab].hash match, so a deep link like "#/guide#margin-vs-markup" keeps its anchor for
    // GettingStarted.
    if (tabFromHash(location.hash) !== tab) {
      history.replaceState(null, "", TABS[tab].hash);
    }
  });

  async function refreshConfig() {
    configFailed = false;
    const result = await loadConfig();
    loginRequired = authRequired; // read right after the call: see lib/api.ts's authRequired doc
    if (loginRequired) {
      appConfig.current = null;
      return;
    }
    if (result) applyConfig(result);
    appConfig.current = result;
    configFailed = result === null;
    // Computed from `result` directly, not the disabledTabs/configUser $derived values, which may not have
    // repropagated within the tick that wrote appConfig.current. No unread count for a tab the nav hides, nor
    // for an identity not granted "requests": that 403 would be swallowed by loadRequests() but not before
    // accessWatch's wrapper permanently flipped the "your access changed" banner for an employee who never
    // lost access.
    const user = extractConfigUser(result);
    if (result && !result.ui.disabledTabs.includes("requests") && hasTabAccess("requests", user)) {
      refreshUnread();
    }
  }

  onMount(refreshConfig);

  // LoginScreen's onLoggedIn: a fresh login invalidates what accessWatch tracked about the previous session
  // (revoked banner, sessionExpired notice). Reset both before re-running the boot flow, or a stale
  // sessionExpired would bounce the new session back to LoginScreen on the next request; they otherwise clear
  // only via resetAccessRevoked() or a full reload.
  function handleLoggedIn() {
    resetAccessRevoked();
    refreshConfig();
  }

  // POST /api/logout (server/auth.go) clears the session cookie; location.reload() re-runs boot, which lands
  // on LoginScreen (the next GET /api/config 401s). Best-effort: reloading lands on LoginScreen even if the
  // request fails. The sidebar sign-out control: an Access sign-in has no printpaca session, so it leaves
  // through Cloudflare's logout URL (a full navigation); everything else uses POST /api/logout.
  const signOut = $derived(signOutAction(configUser, config?.authEnabled === true));

  function signOutNow() {
    if (signOut?.kind === "access") location.assign(signOut.href);
    else void logout();
  }

  async function logout() {
    try {
      await fetch("api/logout", { method: "POST", signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    } catch {
      /* best effort, see doc above */
    }
    location.reload();
  }

  // Reload, tab close and address-bar navigation while an editor holds unsaved edits (unsavedForm: the quote,
  // invoice, item and client editors; settingsDirty: every SettingsEditor). In-app navigation is guarded
  // separately by activate() and syncTabFromHash. The browser shows its own generic prompt.
  function guardUnload(e: BeforeUnloadEvent) {
    if (!unsavedForm.current && !settingsDirty.current && !quoteIsDirty()) return;
    e.preventDefault();
    e.returnValue = "";
  }

  // The print dialog is driven from QuoteTab, which may be unmounted when it closes; clear the job here at
  // the root on afterprint, the one event that fires for both printed and canceled.
  function clearPrintJob() {
    printJob.current = null;
    invoicePrintJob.current = null;
  }

  // The id of each tab's list filter, the target of Ctrl/Cmd+K. A tab with no entry has no filter. The
  // footer hint lists the shortcut only while the element exists (filterPresent below), so a filter that is
  // conditional (Spoolman's spool list, Recent prints) doesn't advertise a no-op.
  const FILTER_IDS: Partial<Record<Tab, string>> = {
    quote: "recent-prints-filter",
    quotes: "quotes-filter",
    clients: "clients-filter",
    invoices: "invoices-filter",
    requests: "requests-filter",
    items: "items-filter",
    filament: "spool-filter",
  };
  let filterPresent = $state(false);
  $effect(() => {
    const id = FILTER_IDS[tab];
    const root = mainEl;
    filterPresent = false;
    if (!id || !root) return;
    const check = () => (filterPresent = document.getElementById(id) instanceof HTMLInputElement);
    check();
    const mo = new MutationObserver(check);
    mo.observe(root, { childList: true, subtree: true });
    return () => mo.disconnect();
  });

  function isTextEntryTarget(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) return false;
    const tag = target.tagName;
    return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
  }

  // Ctrl+S/Cmd+S (no Shift/Alt) saves the page's editor and always preventDefaults, even in a text field.
  // The Quote tab triggers HistoryPanel's primary save action (quoteActions.save); every other editor
  // registers its save through lib/pageShortcuts.svelte.ts (SettingsEditor, the item editor, ...). The
  // actions no-op when saving isn't possible. With no editor registered the browser's own Save runs.
  // Ctrl+K/Cmd+K focuses the tab's own filter (FILTER_IDS); with none rendered it is a silent no-op. It
  // needs a modifier (WCAG 2.1.4: no bare character-key shortcut) and is ignored while focus is in a text
  // field, select or contenteditable.
  function handleGlobalKeydown(e: KeyboardEvent) {
    // Browser autofill dispatches keydown events with no `key`, so check the modifiers first and read the key defensively.
    if (!(e.ctrlKey || e.metaKey) || e.shiftKey || e.altKey) return;
    const isKey = (code: string, letter: string) => e.code === code || e.key?.toLowerCase() === letter;
    if (isKey("KeyS", "s")) {
      if (tab === "quote") {
        e.preventDefault();
        quoteActions.save?.();
      } else if (pageSave.active) {
        e.preventDefault();
        runPageSave();
      }
      return;
    }
    if (!isKey("KeyK", "k")) return;
    const filterId = FILTER_IDS[tab];
    if (!filterId) return;
    if (isTextEntryTarget(e.target)) return;
    const filterInput = document.getElementById(filterId);
    if (!(filterInput instanceof HTMLInputElement)) return; // filter not rendered: silent no-op
    e.preventDefault();
    filterInput.focus();
  }

  // Sidebar sections, grouped by where a tab sits in the shop's lifecycle (sell vs. run the shop), with a
  // header per group (null = ungrouped tail). One nav for every viewport: below 900px .sidebar is a slide-in
  // drawer, the same markup. "guide" is absent on purpose: it is reached from the sidebar footer link (in the
  // drawer too), so it stays reachable at every width; #/guide remains a valid Tab and deep link.
  const NAV_SECTIONS: { label: string | null; items: Tab[] }[] = [
    { label: "Sell", items: ["quote", "dashboard", "quotes", "requests", "invoices", "items", "clients"] },
    { label: "Costs", items: ["filament", "electricity", "printers"] },
    { label: null, items: ["settings"] },
  ];
  // Disabled tabs drop out here (filterNavSections also drops a section header once its items are gone);
  // "quote" and "settings" are never in disabledTabs (server/config.go rejects both), so no section can fully
  // empty. Union'd with ungrantedTabs (permissions.ts) so an employee's nav hides ungranted grantable tabs;
  // "settings" is never grantable, so it stays for every authenticated user (SettingsTab decides what an
  // employee sees).
  const navHidden = $derived(new Set([...disabledTabs, ...ungrantedTabs(Object.keys(TABS) as Tab[], configUser)]));
  const navSections = $derived(filterNavSections(NAV_SECTIONS, navHidden));

  // The sidebar item that is "you are here". A saved quote open in the editor belongs to Quotes, not to New
  // quote, so the highlight (and aria-current) follows the quote's home when that item is in the nav.
  const navCurrent = $derived<Tab>(tab === "quote" && quoteSession.loadedId !== null && !navHidden.has("quotes") ? "quotes" : tab);

  // The sidebar's New quote starts a blank quote (asking first when the open one has unsaved edits), then goes
  // to the editor. Other ways into the editor (Back, the Requests tab's Open in quote, a saved quote's link)
  // go through activate() or the hash and keep what is open.
  function newQuote() {
    if (config && !startNewQuote(config)) return;
    activate("quote");
  }

  // Catches every other way the app sits on a tab it shouldn't: the initial resolution ran before config
  // loaded, a settings save disabled the open tab, or the landing tab isn't granted. replaceState (not
  // activate()) throughout, so it neither fights the data-loss confirm() nor pushes a spurious history entry.
  $effect(() => {
    if (!config) return;
    const disabledFallback = fallbackIfDisabled(tab, disabledTabs, "quote");
    if (disabledFallback !== tab) {
      tab = disabledFallback;
      tabExplicit = false;
      persistTab(disabledFallback);
      history.replaceState(null, "", TABS[disabledFallback].hash);
      return;
    }
    // config (and so configUser) has resolved and `tab` is on something this identity isn't granted. An
    // explicit hash navigation keeps the "No access" card; only the default landing (no hash, or a stale
    // last-visited tab from before a revoked grant) redirects, to the first tab in the filtered nav. With no
    // target (granted nothing at all) it stays on the "No access" card.
    if (tabExplicit || hasTabAccess(tab, configUser)) return;
    const granted = firstNavTab(navSections);
    if (!granted || granted === tab) return;
    tab = granted;
    persistTab(granted);
    history.replaceState(null, "", TABS[granted].hash);
  });

  // Decision 7: the default landing is the Dashboard once any quote, request, or invoice exists; only a fresh
  // install lands on New quote. Runs once, after config loads, and only for the default landing (an explicit
  // link or a remembered tab always wins). A list the user can't open answers 403, which counts as 0. It
  // gives way to anything the user did meanwhile: another tab, or edits already started.
  $effect(() => {
    if (!config || !landedByDefault) return;
    landedByDefault = false;
    if (tab !== "quote" || disabledTabs.has("dashboard") || !hasTabAccess("dashboard", configUser)) return;
    void (async () => {
      const page = { limit: 1, offset: 0 };
      const [quotes, requests, invoices] = await Promise.all([listQuotes(page), loadRequests(page), listInvoices(page)]);
      const activity = { quotes: quotes?.total ?? 0, requests: requests?.total ?? 0, invoices: invoices?.total ?? 0 };
      if (!hasShopActivity(activity)) return;
      if (tab !== "quote" || tabExplicit || settingsDirty.current || unsavedForm.current) return;
      tab = "dashboard";
      persistTab("dashboard");
      history.replaceState(null, "", TABS.dashboard.hash);
    })();
  });

  const spoolmanEnabled = $derived(appConfig.current?.integrations.spoolman === true);
  const moonrakerConfigured = $derived(appConfig.current?.moonraker === true);
  // Integration status is for people who can use or change the integration: an admin, or a user holding a
  // grant for a page that depends on it (Spoolman: the spool picker on New quote, the Filament page;
  // Moonraker: Recent prints on New quote). Anyone else would see status they can't act on.
  const showSpoolmanLine = $derived(hasTabAccess("quote", configUser) || hasTabAccess("filament", configUser));
  const showMoonrakerLine = $derived(hasTabAccess("quote", configUser));
  const versionLabel = $derived(appConfig.current?.version ? `printpaca ${appConfig.current.version}` : "printpaca dev");
  // DEMO_MODE=1 server-side (server/main.go), a plain field on the loaded config like authEnabled.
  const demoMode = $derived(config?.demoMode === true);

  // demoLocal.svelte.ts is inert until this runs, so the non-demo app never touches localStorage or the DOM
  // through it. Gated on demoMode alone; initDemoLocal() is idempotent, so real work happens once.
  $effect(() => {
    if (demoMode) initDemoLocal();
  });

  // The platform doesn't change mid-session, so compute once. userAgentData.platform (Chromium) is preferred,
  // falling back to navigator.platform (Safari/Firefox); it names the modifier that triggers HistoryPanel's
  // save.
  const isMac = (
    (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ??
    navigator.platform ??
    ""
  )
    .toUpperCase()
    .includes("MAC");
  const shortcutMod = isMac ? "Cmd" : "Ctrl";
  const shortcutHints = $derived(
    [filterPresent ? `${shortcutMod}+K filter` : "", tab === "quote" || pageSave.active ? `${shortcutMod}+S save` : ""].filter(Boolean),
  );

  // Demo banner height -> --demo-banner-h, so html's scroll-padding-top keeps a focused field from sitting
  // behind the sticky banner when the window scrolls (below 900px; at >=900px the banner is its own grid
  // row outside <main>). WCAG 2.4.11.
  let demoBannerEl: HTMLElement | undefined = $state();
  $effect(() => {
    const el = demoBannerEl;
    if (!el) return;
    const apply = () => document.documentElement.style.setProperty("--demo-banner-h", isDesktop ? "0px" : `${el.offsetHeight}px`);
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(el);
    return () => {
      ro.disconnect();
      document.documentElement.style.removeProperty("--demo-banner-h");
    };
  });

  // Empty when Moonraker/Fluidd isn't configured (fluiddUrl returns "" for a null config or no fluiddUrl);
  // the sidebar link renders only when non-empty.
  const fluiddHref = $derived(fluiddUrl(config));

  // Page title (WCAG 2.4.2): the login screen, else the active tab; a detail view prepends its record name
  // (lib/pageTitle.svelte.ts). The detail view clears it when it closes.
  const pageTitle = $derived(
    loginRequired || accessWatch.sessionExpired ? "Sign in" : pageTitleState.record ? `${pageTitleState.record} · ${TABS[tab].label}` : TABS[tab].label,
  );

  // Branding: the business name and logo from Settings (config.quote), falling back to printpaca and the
  // bundled mark, including when the logo URL fails to load. appConfig is refreshed after every Settings save,
  // so a change shows without a reload. The login screen has no config yet and uses the public branding
  // (GET /api/public/config) when the server offers it, else the fallbacks.
  const onLoginScreen = $derived(loginRequired || accessWatch.sessionExpired);
  let publicBranding = $state<{ businessName: string; logoUrl: string } | null>(null);
  $effect(() => {
    if (!onLoginScreen || publicBranding) return;
    let cancelled = false;
    void loadPublicConfig().then((c) => {
      if (!cancelled && c) publicBranding = { businessName: c.branding.businessName, logoUrl: c.branding.logoUrl };
    });
    return () => (cancelled = true);
  });
  const brandSource = $derived(onLoginScreen ? publicBranding : (config?.quote ?? null));
  const businessName = $derived(brandSource?.businessName.trim() || "printpaca");
  // The URL that last failed to load, so the fallback holds for that URL only and a corrected URL is retried.
  let failedLogoUrl = $state("");
  const brandLogoSrc = $derived(brandSource?.logoUrl && brandSource.logoUrl !== failedLogoUrl ? brandSource.logoUrl : logoUrl);

  // The brand link goes to the Dashboard (D22) for anyone whose nav has it, else to the first tab in their nav
  // (New quote for most people), like the Home link on a site.
  const homeTab = $derived(brandTab(navSections, "dashboard") ?? "quote");
  // Moves focus to the view's h1 (tabindex -1 so it can take script focus), the same landing spot a screen
  // reader user gets from the skip link's neighbours. activate() has already closed the drawer and queued its
  // own focus on <main>; this runs after the new tab has rendered and refines it.
  async function goHome(e: MouseEvent) {
    // Let the browser handle a modified click (open in a new tab or window) as a plain link.
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    activate(homeTab);
    if (tab !== homeTab) return; // the unsaved-changes confirm was declined: stay put
    await tick();
    await tick();
    const h1 = mainEl?.querySelector<HTMLElement>("h1");
    if (h1) {
      h1.tabIndex = -1;
      h1.focus({ preventScroll: true });
    }
  }
</script>

<svelte:head>
  <title>{pageTitle} · {businessName}</title>
</svelte:head>

<LiveAnnouncer />

<svelte:window onhashchange={syncTabFromHash} onafterprint={clearPrintJob} onkeydown={handleGlobalKeydown} onbeforeunload={guardUnload} />

{#snippet brand(showName: boolean)}
  <!-- The accessible name is set on the link, so a collapsed sidebar (logo only) still names it. It contains
       the visible name (WCAG 2.5.3). -->
  <a class="brand" href={TABS[homeTab].hash} aria-label={`${businessName} home`} onclick={goHome}>
    <img class="logo" src={brandLogoSrc} alt="" onerror={() => (failedLogoUrl = brandLogoSrc)} />
    {#if showName}<span class="brand-name">{businessName}</span>{/if}
  </a>
{/snippet}

{#snippet navButton(id: Tab, collapsed: boolean)}
  <button
    type="button"
    class:active={navCurrent === id}
    aria-current={navCurrent === id ? "page" : undefined}
    title={collapsed ? TABS[id].label : undefined}
    onclick={() => (id === "quote" ? newQuote() : activate(id))}
  >
    <Icon path={TABS[id].icon} />
    <!-- The label is always in the DOM (visually hidden when collapsed), so the button's accessible name is its
         visible text plus the unread count below (WCAG 1.3.1, 2.5.3). -->
    <span class="label" class:visually-hidden={collapsed}>{TABS[id].label}</span>
    {#if id === "requests" && unreadRequests.count > 0}
      <span class="unread-pill">{unreadRequests.count}<span class="visually-hidden"> unread requests</span></span>
    {/if}
  </button>
{/snippet}

{#snippet sectionedNav(collapsed: boolean)}
  {#each navSections as section, i (i)}
    {#if section.label && !collapsed}
      <div role="group" aria-labelledby="nav-sec-{i}" class="nav-group">
        <div class="section-label nav-section-label" id="nav-sec-{i}">{section.label}</div>
        {#each section.items as id (id)}
          {@render navButton(id, collapsed)}
        {/each}
      </div>
    {:else}
      {#each section.items as id (id)}
        {@render navButton(id, collapsed)}
      {/each}
    {/if}
  {/each}
{/snippet}

{#if loginRequired || accessWatch.sessionExpired}
  <LoginScreen onLoggedIn={handleLoggedIn} {businessName} logoSrc={brandLogoSrc} onLogoError={() => (failedLogoUrl = brandLogoSrc)} notice={accessWatch.sessionExpired ? SESSION_EXPIRED_NOTICE : undefined} />
{:else}
<a class="skip-link" href="#main" onclick={skipToMain}>Skip to content</a>

{#if demoMode}
  <!-- Shell-level, rendered once here instead of inside each per-tab branch of <main>. The server already
       rejects every write in demo mode (server/auth.go's demoWriteGuard); this banner only sets
       expectations and gates no nav item or button. -->
  <div class="banner-muted demo-banner" inert={drawerOpen && !isDesktop} onwheel={forwardWheelToMain} bind:this={demoBannerEl}>
    <span role="status">Read-only demo. Changes are disabled and nothing is saved. Currency and user mode only change what you see: amounts aren't converted, and your sign-in stays the same.</span>
    <!-- Cosmetic per-browser display prefs only (demoLocal.svelte.ts): nothing reaches the server and
         neither select gates any nav item or button. -->
    <span class="demo-controls">
      <select
        class="demo-control"
        aria-label="Theme"
        value={demoLocal.theme}
        onchange={(e) => setDemoTheme(e.currentTarget.value as ThemeChoice)}
      >
        <option value="system">System</option>
        <option value="light">Light</option>
        <option value="dark">Dark</option>
      </select>
      <select
        class="demo-control"
        aria-label="Currency"
        value={demoLocal.currency ?? ""}
        onchange={(e) => setDemoCurrency((e.currentTarget.value || null) as CurrencyPresetId | null)}
      >
        <option value="">Demo default</option>
        {#each CURRENCY_PRESET_OPTIONS as opt (opt.id)}
          <option value={opt.id}>{opt.label}</option>
        {/each}
      </select>
      <!-- User mode: presentation only. Switches whether the Users card, the Account card's multi-user
           parts and attribution lines show, backed by the demo's seeded employees. Never gates a nav
           item, route or write; the server blocks every write in demo mode regardless. -->
      <select
        class="demo-control"
        aria-label="User mode"
        value={demoLocal.userMode}
        onchange={(e) => setDemoUserMode(e.currentTarget.value as UserModeChoice)}
      >
        <option value="single">User mode: single</option>
        <option value="multi">User mode: multi</option>
      </select>
    </span>
  </div>
{/if}

<header inert={drawerOpen && !isDesktop}>
  <!-- Mobile-only top bar (hidden >=900px): the hamburger opens .sidebar as a drawer instead of this
       header carrying a second copy of the nav. aria-controls names the drawer; aria-expanded mirrors
       drawerOpen. -->
  <button
    type="button"
    class="hamburger"
    aria-expanded={drawerOpen}
    aria-controls="app-drawer"
    aria-label={`${drawerOpen ? "Close" : "Open"} menu${unreadRequests.count > 0 ? `, ${unreadRequests.count} unread requests` : ""}`}
    bind:this={hamburgerEl}
    onclick={toggleDrawer}
  >
    <Icon path={mdiMenu} />
    <span class="hamburger-label" aria-hidden="true">Menu</span>
    {#if unreadRequests.count > 0}
      <!-- Decorative dot, not a count: the drawer shows the real number on the Requests nav button once
           opened; this only says "something's waiting". -->
      <span class="hamburger-badge" aria-hidden="true"></span>
    {/if}
  </button>
  {@render brand(true)}
</header>

{#if drawerOpen}
  <!-- Dismiss backdrop: in the DOM only while open, behind the drawer's z-index but above everything
       else, including the sticky demo banner. -->
  <button type="button" class="drawer-backdrop" aria-label="Close menu" onclick={() => closeDrawer()}></button>
{/if}

<aside
  id="app-drawer"
  class="sidebar"
  class:collapsed={sidebarCollapsed}
  class:open={drawerOpen}
  bind:this={asideEl}
  onwheel={forwardWheelToMain}
  inert={!isDesktop && !drawerOpen}
>
  {@render brand(!navCollapsed)}
  <nav class="side-nav" aria-label="Sections">
    {@render sectionedNav(navCollapsed)}
    {#if fluiddHref}
      <!-- Link-only, never proxied (server/main.go validates FLUIDD_URL at startup but never fetches it):
           a plain external anchor styled like the buttons above, with the open-in-new icon marking it
           external. Reachable at every width, as part of the same drawer. -->
      <a
        class="nav-external"
        href={fluiddHref}
        target="_blank"
        rel="noopener noreferrer"
        title={navCollapsed ? "Fluidd (opens in a new tab)" : undefined}
        aria-label="Fluidd (opens in a new tab)"
        onclick={() => closeDrawer(false)}
      >
        <Icon path={mdiOpenInNew} />
        <span class="label" class:visually-hidden={navCollapsed}>Fluidd</span>
      </a>
    {/if}
  </nav>
  <div class="sidebar-footer">
    <!-- Only configured integrations get a line; an unconfigured one is status the owner already knows
         and never acts on here. Renders nothing when neither is configured. -->
    {#if spoolmanEnabled && showSpoolmanLine}
      <div class="integration-line" title={navCollapsed ? "Spoolman configured" : undefined}>
        <span class="dot ok" aria-hidden="true"></span>
        {#if !navCollapsed}<span>Spoolman configured</span>{:else}<span class="visually-hidden">Spoolman configured</span>{/if}
      </div>
    {/if}
    {#if moonrakerConfigured && showMoonrakerLine}
      <div class="integration-line" title={navCollapsed ? "Moonraker configured" : undefined}>
        <span class="dot ok" aria-hidden="true"></span>
        {#if !navCollapsed}<span>Moonraker configured</span>{:else}<span class="visually-hidden">Moonraker configured</span>{/if}
      </div>
    {/if}
    {#if !navCollapsed}
      {#if !disabledTabs.has("guide")}
        <button
          type="button"
          class="guide-footer-link"
          class:active={tab === "guide"}
          aria-current={tab === "guide" ? "page" : undefined}
          onclick={() => activate("guide")}
        >Guide</button>
      {/if}
      {#if signOut}
        <button type="button" class="guide-footer-link" onclick={signOutNow}>{signOut.label}</button>
      {/if}
      <!-- Admin-only, per browser (localStorage). The public pages never read it. -->
      <button type="button" class="guide-footer-link" aria-pressed={density.current === "compact"} onclick={toggleDensity}>Compact density</button>
      <div class="version-line">{versionLabel}</div>
    {:else}
      {#if !disabledTabs.has("guide")}
        <button type="button" class="guide-footer-icon" aria-label="Guide" onclick={() => activate("guide")}>
          <Icon path={mdiBookOpenVariant} size={16} />
        </button>
      {/if}
      {#if signOut}
        <button type="button" class="guide-footer-icon" aria-label={signOut.label} onclick={signOutNow}>
          <Icon path={mdiLogout} size={16} />
        </button>
      {/if}
      <button type="button" class="guide-footer-icon" aria-label="Compact density" aria-pressed={density.current === "compact"} onclick={toggleDensity}>
        <Icon path={mdiFormatLineSpacing} size={16} />
      </button>
    {/if}
    {#if isDesktop}
      <!-- Icon-only collapsing is a desktop concept: below 900px the drawer is near full width (see
           navCollapsed), where icons would save no space and the toggle would be one more control to
           explain. -->
      <button
        type="button"
        class="collapse-toggle"
        aria-label={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
        onclick={toggleSidebar}
      >
        <Icon path={sidebarCollapsed ? mdiChevronRight : mdiChevronLeft} size={16} />
        {#if !sidebarCollapsed}<span>Collapse sidebar</span>{/if}
      </button>
    {/if}
  </div>
</aside>

<!-- tabindex 0 on the Guide only: its scroll container has no focusable content, so keyboard users could not scroll it (WCAG 2.1.1). -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<main class="app-main" id="main" tabindex={tab === "guide" ? 0 : -1} inert={drawerOpen && !isDesktop} aria-label={TABS[tab].label} bind:this={mainEl}>
  <div class="main-inner">
  {#if accessWatch.revoked}
    <!-- Global 403 handling: a permission that worked just answered 403 (accessWatch.svelte.ts), most
         likely because an admin revoked a grant or turned multi-user mode off mid-session. Reloading
         re-runs the boot flow (LoginScreen if the session died, else a fresh /api/config). Rendered above
         every other state, even a stale configFailed. -->
    <div class="card banner-warn" role="alert">
      <p>Your access changed. Reload the page to pick up your current permissions.</p>
      <button type="button" onclick={() => location.reload()}>Reload</button>
    </div>
  {/if}
  {#if configFailed}
    <div class="card banner-error" role="alert">
      <p>Couldn't load configuration from the server. Check that printpaca's backend is running.</p>
      <button onclick={refreshConfig}>Retry</button>
    </div>
  {:else if !config}
    <p role="status">Loading…</p>
  {:else if !hasTabAccess(tab, configUser)}
    <!-- Direct hash navigation (or a stale last-visited tab) onto a tab this identity isn't granted gets
         a "no access" note: not a blank page, and never a silent redirect (a disabled tab gets that,
         being a config choice the visitor had no part in). The nav never links here (navHidden), so
         reaching it means a hand-edited address bar, a bookmark predating a revoked grant, or a shared
         link. -->
    <div class="card banner-warn" role="alert">
      <h2>No access</h2>
      <p>You don't have access to the {TABS[tab].label} page. Ask an admin to grant it to you.</p>
    </div>
  {:else if tab === "guide"}
    <GettingStarted />
  {:else if tab === "quote"}
    <QuoteTab cfg={config} />
  {:else if tab === "quotes"}
    <QuotesTab cfg={config} user={configUser} />
  {:else if tab === "electricity"}
    <ElectricityTab cfg={config} />
  {:else if tab === "filament"}
    <FilamentTab cfg={config} />
  {:else if tab === "printers"}
    <PrintersTab />
  {:else if tab === "clients"}
    <ClientsTab cfg={config} />
  {:else if tab === "invoices"}
    <InvoicesTab cfg={config} user={configUser} />
  {:else if tab === "items"}
    <ItemsTab cfg={config} />
  {:else if tab === "requests"}
    <RequestsTab cfg={config} onOpenInQuote={() => activate("quote")} onOpenInvoice={() => activate("invoices")} />
  {:else if tab === "dashboard"}
    <DashboardTab cfg={config} />
  {:else}
    <SettingsTab />
  {/if}

  <footer>
    <span>printpaca, an open-source 3D print cost and quote calculator.</span>
    <!-- Lists only the shortcuts that work on the current page: {shortcutMod}+S where an editor registered a
         save, {shortcutMod}+K where the page has a filter input (see FILTER_IDS). Lives in the page footer,
         not the sidebar's, so the sidebar keeps room for every destination on short windows. -->
    {#if shortcutHints.length > 0}
      <div class="shortcuts-line">Shortcuts: {shortcutHints.join(", ")}</div>
    {/if}
  </footer>
  </div>
</main>

{#if printJob.current && config}
  <PrintView
    cfg={config}
    order={printJob.current.order}
    mode={printJob.current.mode}
    meta={printJob.current.meta}
  />
{:else if invoicePrintJob.current && config}
  <InvoicePrintView cfg={config} invoice={invoicePrintJob.current} />
{/if}
{/if}

<style>
  :global(#app) {
    min-height: 100vh;
    /* Containing block for every position:absolute descendant (skip-link, .visually-hidden labels/inputs).
     * Without it, one escaping #app lays out outside its box; at >=900px (#app is 100vh + overflow:hidden)
     * its extent still counts toward documentElement.scrollHeight, so a wheel over the sidebar scrolls the
     * whole grid shell off-screen. */
    position: relative;
  }

  /* Hidden until focused (Tab from a fresh load lands here first); a high z-index and on-brand background
   * keep it legible over the header. */
  .skip-link {
    position: absolute;
    top: -3rem;
    left: var(--space-2);
    z-index: 200;
    padding: var(--space-2) var(--space-4);
    background: var(--accent);
    color: var(--accent-contrast);
    border-radius: var(--radius);
    transition: top 0.15s ease;
  }
  .skip-link:focus {
    top: var(--space-2);
  }

  /* Edge-to-edge strip, not an inset card like .banner-muted's other callers: no radius, centered text,
   * sticky to the viewport top. At >=900px it becomes its own grid row above "side main", which pins it
   * without position:sticky (harmless to leave set). */
  .demo-banner {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: center;
    gap: var(--space-1) var(--space-2);
    border-radius: 0;
    border-width: 0 0 1px;
    text-align: center;
    position: sticky;
    top: 0;
    z-index: 150;
  }
  /* Wraps under the banner text at narrow widths instead of squeezing or overflowing; .demo-banner's
   * flex-wrap handles the reflow, so no breakpoint is needed. */
  .demo-controls {
    display: inline-flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: var(--space-2);
  }
  /* Overrides the app-wide select { min-height: 44px } (a class beats a type selector): the banner is a
   * thin strip, so its controls read as compact inline pickers (like button.quiet's --tap-sm). */
  .demo-control {
    min-height: var(--tap-sm);
    padding: 0 var(--space-2);
    font-size: var(--text-xs);
  }

  /* The phone top bar and the sidebar share one tint (D6, D11): --surface on the --bg page, no outline. */
  header {
    display: flex; align-items: center; gap: var(--space-3);
    padding: var(--space-2) var(--space-4);
    background: var(--surface);
  }
  .brand { display: flex; align-items: center; gap: var(--space-2); color: inherit; text-decoration: none; min-width: 0; border-radius: var(--radius); }
  .brand .brand-name { font-size: var(--type-heading); font-weight: 700; margin: 0; overflow-wrap: anywhere; }
  .brand:hover .brand-name { text-decoration: underline; }
  .logo { height: 40px; width: auto; display: block; }

  /* Mobile top bar's menu button. position: relative anchors .hamburger-badge; min-width matches the global
   * `button { min-height: 44px }` for a square tap target. */
  .hamburger {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 44px;
    gap: var(--space-1);
    padding: 0 var(--space-3);
    border: none;
    background: none;
    border-radius: var(--radius);
    color: var(--text);
    flex-shrink: 0;
  }
  /* The visible word (WCAG 2.5.3: the accessible name "Open menu" contains it) so the icon alone isn't the
   * only cue. */
  .hamburger-label { font-size: var(--text-sm); font-weight: 600; }
  .hamburger:hover { background: var(--surface-2); }
  /* Decorative badge over the menu glyph's corner, in the same accent as navButton's unread-pill in the
   * drawer it opens (see the hamburger markup for why it is a dot). */
  .hamburger-badge {
    position: absolute;
    top: var(--space-2);
    right: var(--space-2);
    width: var(--space-2);
    height: var(--space-2);
    border-radius: 50%;
    background: var(--accent);
  }

  /* Backdrop behind the open drawer: covers and dims the viewport, above everything (including the sticky
   * demo banner's z-index: 150) but below the drawer (z-index: 200). A <button>, so it is a real, labeled,
   * keyboard-reachable control. */
  .drawer-backdrop {
    position: fixed;
    inset: 0;
    z-index: 199;
    border: none;
    padding: 0;
    background: rgb(0 0 0 / 45%);
  }

  /* The list scrolls inside the sidebar when the window is short. The two local gradients are masks that
   * cover the shadows once the list is scrolled to that edge, so a shadow shows only where more items hide. */
  .side-nav {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    margin-top: var(--space-2);
    overflow-y: auto;
    min-height: 0;
    background:
      linear-gradient(var(--surface) 30%, transparent) top / 100% 1.5rem no-repeat local,
      linear-gradient(transparent, var(--surface) 70%) bottom / 100% 1.5rem no-repeat local,
      radial-gradient(farthest-side at 50% 0, rgb(0 0 0 / 28%), transparent) top / 100% 0.6rem no-repeat scroll,
      radial-gradient(farthest-side at 50% 100%, rgb(0 0 0 / 28%), transparent) bottom / 100% 0.6rem no-repeat scroll;
  }
  .nav-group { display: flex; flex-direction: column; gap: var(--space-1); }
  .side-nav button, .side-nav a.nav-external { width: 100%; }
  /* Fluidd's external link: same box model and spacing as the nav buttons (shares their
   * border-left/padding/gap rules), plus a text-decoration reset since it is an <a>. */
  .side-nav a.nav-external {
    color: var(--text);
    text-decoration: none;
  }
  .side-nav a.nav-external:hover { color: var(--accent-text); }
  .side-nav button, .side-nav a.nav-external {
    /* A transparent 3px border-left reserves the active indicator's space on every button, so activating
     * one doesn't shift the icon and label. */
    border: none; border-left: 3px solid transparent;
    background: none; padding: var(--space-1) var(--space-3); border-radius: var(--radius);
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }
  .side-nav button:hover, .side-nav a.nav-external:hover { background: var(--surface-2); }
  .side-nav button.active {
    background: var(--surface-2);
    font-weight: 600;
    border-left: 3px solid var(--accent);
  }

  .unread-pill {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: var(--space-5);
    padding: 0 var(--space-1);
    border-radius: 999px;
    background: var(--accent);
    color: var(--accent-contrast);
    font-size: var(--text-xs);
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    line-height: 1.4;
  }

  /* Group headers ("Sell", "Shop") in the one nav, at every width. Hidden only when the sidebar is
   * collapsed to icons on desktop (navCollapsed/sectionedNav); the drawer never collapses. */
  .nav-section-label {
    padding: var(--space-3) var(--space-2) var(--space-1);
  }
  .nav-section-label:first-child { padding-top: 0; }

  /* --content-max (1440px, D10), not the viewport: past roughly this width a card becomes a band with its contents stranded at
   * one end (summary stats, list names, a drop zone's one line). The Guide already capped its measure for
   * this reason; this applies the cap to every tab. Below ~1500px viewports nothing changes. */
  /* Sticky bars can cover a focused field in the scroll container (WCAG 2.4.11). The Settings jump nav (top),
   * the Settings save bar and the quote history save row (bottom) publish their measured heights through the
   * stickyInset action as --sticky-top-h and --sticky-bottom-h; the demo banner publishes --demo-banner-h
   * below 900px. The scroller is main at >=900px and the window below that, so both get the same padding. */
  main,
  :global(html) {
    scroll-padding-top: calc(var(--demo-banner-h, 0px) + var(--sticky-top-h, 0px) + 1rem);
    scroll-padding-bottom: calc(var(--sticky-bottom-h, 0px) + 1rem);
  }
  /* <main> spans its whole grid cell so a wheel or a scrollbar works anywhere across the width; the 1440px cap,
   * centring and padding live on this inner wrapper. <main> has no padding of its own, so a sticky bar at
   * top: 0 sits flush with the scrollport edge instead of 1rem below it. */
  .main-inner { max-width: var(--content-max); margin: 0 auto; padding: var(--space-4); }
  @media (min-width: 900px) {
    .main-inner { padding-inline: var(--space-6); }
  }
  /* theme.css's global :focus-visible outline draws outside the element (outline-offset: 2px); #app clips
   * overflow, so on <main> (the skip-link target and scroll container) it gets cut into a stray partial
   * line. Pull the ring inward. */
  #main:focus-visible { outline-offset: -2px; }
  /* Script focus (skip link, nav activation, the brand link) lands on <main> and on view headings, all
   * tabindex="-1": targets for moving the reading position, not controls. Chromium still matches
   * :focus-visible for them when the trigger was the keyboard, which framed the whole content area in a ring.
   * They take no ring; real controls (and Guide's tabindex="0" scroll region) keep theme.css's. */
  #main[tabindex="-1"]:focus-visible,
  :global(h1[tabindex="-1"]:focus-visible),
  :global(h2[tabindex="-1"]:focus-visible) { outline: none; }
  /* Lives inside <main> as its last child, not a separate grid row: content ends where it ends and the
   * footer follows, instead of a fixed row pinning it to the viewport bottom with a void above on short
   * pages. The top border/margin separate it. No max-width or horizontal margin of its own: it's already
   * inside <main>'s, and adding them would double the indent versus every card. */
  footer {
    margin-top: var(--space-6);
    padding: var(--space-4) 0;
    color: var(--muted);
    font-size: var(--text-xs);
  }

  /* Sidebar shell below 900px: the same markup the >=900px block turns into a pinned column is a fixed,
   * full-height drawer hidden off the left edge (translateX(-100%)) until .open slides it in. Scoped to
   * this one media query, not an unscoped base rule the >=900px block overrides, so none of it reaches the
   * >=900px layout. */
  @media (max-width: 899.98px) {
    .sidebar {
      display: flex;
      flex-direction: column;
      position: fixed;
      inset-block: 0;
      left: 0;
      width: var(--drawer-w);
      z-index: 200;
      padding: var(--space-4) var(--space-3);
      gap: var(--space-2);
      background: var(--surface);
      overflow: hidden;
      transform: translateX(-100%);
      transition: transform 0.2s ease;
    }
    .sidebar.open { transform: translateX(0); }
  }
  @media (max-width: 899.98px) and (prefers-reduced-motion: reduce) {
    .sidebar { transition: none; }
  }

  /* Background scroll lock while the drawer is open (see the $effect toggling this class): the window
   * scrolls below 900px, so this is the one place that needs it, and the drawer can't open above 900px
   * (an effect on isDesktop closes it on resize). */
  @media (max-width: 899.98px) {
    :global(body.drawer-open) { overflow: hidden; }
  }

  .sidebar-footer {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    margin-top: auto;
    padding-top: var(--space-3);
  }
  /* Named .integration-line, not .status-line: theme.css's .status-line is the shared live-region class
   * (verifying…, saving…), and this static Spoolman dot + text isn't a live region, so reusing the name
   * would be a collision. */
  .integration-line {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    color: var(--muted);
    font-size: var(--text-xs);
  }
  .dot {
    width: var(--space-2);
    height: var(--space-2);
    border-radius: 50%;
    background: var(--border);
    flex-shrink: 0;
  }
  .dot.ok { background: var(--ok); }
  /* Accent-colored and underlined, unlike the muted status lines around it, so it reads as an actionable
   * link. align-self: flex-start sizes the click target to its text (sidebar-footer is a column flex
   * container, where buttons stretch); min-height brings the target nearer a reasonable tap size. */
  .guide-footer-link {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    min-height: var(--tap-sm);
    border: none;
    background: none;
    padding: 0;
    text-align: left;
    color: var(--accent-text);
    text-decoration: underline;
    font-size: var(--text-xs);
    cursor: pointer;
  }
  .guide-footer-link:hover { color: var(--text); }
  /* Phone drawer: footer links are touch targets too (D26). */
  @media (max-width: 600px) {
    .guide-footer-link { min-height: 44px; }
  }
  /* The same "this is where you are" affordance as the nav buttons (.side-nav button.active), adapted for a
   * text link: bold weight and solid --text color instead of a background fill, which would look out of
   * place on this thin footer link. */
  .guide-footer-link.active,
  .guide-footer-link[aria-pressed="true"] {
    font-weight: 700;
    color: var(--text);
  }
  /* Collapsed-sidebar counterpart: same destination, icon-only like the nav buttons' collapsed treatment,
   * since there is no room for a text link. */
  .guide-footer-icon {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: var(--tap-sm);
    height: var(--tap-sm);
    border: none;
    background: none;
    padding: 0;
    color: var(--muted);
    cursor: pointer;
  }
  .guide-footer-icon:hover { color: var(--text); }
  .version-line { color: var(--muted); font-size: var(--text-xs); }
  /* Short windows (1366x768 and 1366x860 laptops): tighter nav rows and footer so every destination, Settings included,
   * fits without scrolling. The nav still scrolls, with its shadow, below this. */
  @media (min-width: 900px) and (max-height: 900px) {
    .side-nav { gap: 0; }
    .side-nav button, .side-nav a.nav-external { padding-block: 0; min-height: var(--tap-sm); }
    .nav-section-label { padding-block: var(--space-2) 0; }
    .sidebar { padding-block: var(--space-3); gap: var(--space-1); }
    .sidebar-footer { gap: var(--space-1); padding-top: var(--space-2); }
  }
  .shortcuts-line { color: var(--muted); font-size: var(--text-xs); }
  .collapse-toggle {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    align-self: flex-start;
    border: none;
    background: none;
    padding: var(--space-1) var(--space-2);
    border-radius: var(--radius);
    color: var(--muted);
    font-size: var(--text-xs);
  }
  .collapse-toggle:hover { color: var(--text); }

  @media (min-width: 900px) {
    header { display: none; }
    /* Fixed height (not min-height) so the shell never grows past the viewport and never scrolls; <main> is
     * the one scrolling region, so the sidebar (grid-area: side) stays viewport-pinned however tall a page
     * gets. A single row: the page footer lives inside <main> as ordinary content, directly after the tab's
     * content. */
    :global(#app) {
      display: grid;
      grid-template-columns: auto 1fr;
      /* "banner" is its own row above "side main", so the demo banner sits pinned above both sidebar and
       * main, outside main's scroll region. Sized "auto": with no banner (demoMode false) it collapses to 0
       * height. */
      grid-template-areas: "banner banner" "side main";
      grid-template-rows: auto 1fr;
      height: 100vh;
      overflow: hidden;
    }
    /* Print undo for the grid above. theme.css's @media print block also sets #app { display: block; ... },
     * but component styles bundle after theme.css, so at >=900px this grid rule would win under print and
     * auto-place #print-view into the "side" column (a squeezed half-width printout). The undo must live
     * here, after the grid declaration, to win the cascade; values mirror theme.css's print block. */
    @media print {
      :global(#app) {
        display: block;
        height: auto;
        overflow: visible;
      }
    }
    .demo-banner {
      grid-area: banner;
    }
    .sidebar {
      display: flex;
      flex-direction: column;
      grid-area: side;
      width: var(--sidebar-w);
      padding: var(--space-4) var(--space-3);
      gap: var(--space-2);
      background: var(--surface);
      /* 100% of the grid row, NOT 100vh: with the demo banner rendered the "side main" row is viewport
       * height minus the banner, so a 100vh sidebar overflows it. #app's overflow:hidden clips that
       * visually but leaves programmatic scroll range, so scrollIntoView (guide anchor jumps) scrolled #app
       * and shoved the banner off-screen. */
      height: 100%;
      overflow: hidden;
    }
    .sidebar.collapsed { width: var(--sidebar-w-collapsed); padding: var(--space-4) var(--space-2); }
    .sidebar.collapsed .brand { justify-content: center; }
    /* .side-nav's base rule (shared with the drawer) already has overflow-y: auto, so the nav list scrolls
     * internally when it outgrows the viewport, keeping the footer below it reachable; nothing is added
     * here. */
    .sidebar.collapsed .side-nav button, .sidebar.collapsed .side-nav a.nav-external { justify-content: center; padding: var(--space-1); }
    /* The scrolling region: everything taller than the viewport (the tab's content, then the page footer)
     * scrolls inside <main> instead of the page, which keeps the sidebar pinned. <main> fills its grid
     * cell (the scrollbar sits at the window edge); .main-inner caps and centres the content. min-width: 0
     * lets wide content scroll inside the cell instead of widening the grid column. */
    main { grid-area: main; overflow-y: auto; min-width: 0; }
  }
</style>
