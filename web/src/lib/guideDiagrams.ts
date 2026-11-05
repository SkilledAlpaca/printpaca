// The data behind the Guide's two diagrams. GuideDiagram.svelte draws them as inline SVG from theme tokens and
// renders the same data as a table or list for small screens and screen readers, so the picture and its text
// alternative can never disagree. Keys are checked against server/config.go and docs/configuration.md in
// guideDiagrams.test.ts.

export interface SettingsMapRow {
  /** Where you edit it in the app. */
  where: string;
  /** The config.yaml key (or keys). */
  keys: string[];
}

/** Settings page cards, in the order of the page. */
export const SETTINGS_PAGE_ROWS: SettingsMapRow[] = [
  { where: "Currency", keys: ["currency"] },
  { where: "Pricing", keys: ["pricing"] },
  { where: "Time zone", keys: ["timezone"] },
  { where: "Fee schedules", keys: ["feeSchedules"] },
  { where: "Hardware", keys: ["hardware"] },
  { where: "Defaults", keys: ["defaults"] },
  { where: "Business identity", keys: ["quote"] },
  { where: "Storefront", keys: ["shop", "site", "publicQuotes"] },
  { where: "Custom CSS", keys: ["customCss"] },
  { where: "Email", keys: ["smtp"] },
  { where: "Integrations", keys: ["integrationsUrls"] },
  { where: "Users", keys: ["users"] },
  { where: "Features", keys: ["ui"] },
];

/** Settings that live on the page they affect rather than on Settings. */
export const OTHER_PAGE_ROWS: SettingsMapRow[] = [
  { where: "Filament page", keys: ["materials"] },
  { where: "Printers page", keys: ["printers", "presets"] },
  { where: "Electricity page", keys: ["energy"] },
  { where: "Invoices page", keys: ["invoicing"] },
];

export interface EnvRow {
  name: string;
  what: string;
}

/** Values with no field in the app, read when the server starts. */
export const ENV_ONLY_ROWS: EnvRow[] = [
  { name: "PORT", what: "main port for the shop and the admin app" },
  { name: "CONFIG_DIR", what: "folder that holds config.yaml, the database, and uploads" },
  { name: "ADMIN_PASSWORD", what: "admin sign-in password" },
  { name: "SMTP_PASSWORD", what: "email password, never stored in config.yaml" },
  { name: "PUBLIC_PORT", what: "optional customer-only port" },
  { name: "ALLOWED_HOSTS", what: "host names the server accepts" },
  { name: "ADMIN_TRUST_PROXY", what: "trust one reverse proxy in front of the admin app" },
];

export type FlowTone = "step" | "ok" | "warn";
export interface FlowBox {
  id: string;
  title: string;
  detail: string;
  tone: FlowTone;
}

/** The main path of saving Settings, left to right. */
export const SAVE_MAIN: FlowBox[] = [
  { id: "edit", title: "Edit", detail: "Change a field. The edit stays on this page.", tone: "step" },
  { id: "bar", title: "Save bar", detail: "Discard changes and Save changes appear at the bottom.", tone: "step" },
  { id: "save", title: "Save changes", detail: "Press it, or press Ctrl+S.", tone: "step" },
  { id: "validate", title: "Validate", detail: "The server checks every value.", tone: "step" },
  { id: "applied", title: "Applied", detail: "Written to config.yaml and live at once. \"Settings saved.\"", tone: "ok" },
];

/** What can go wrong, each hanging off the step that detects it. */
export const SAVE_BRANCHES: { from: string; box: FlowBox }[] = [
  { from: "validate", box: { id: "errors", title: "Errors", detail: "Nothing is written. The save bar lists each problem with a link to its field. Fix it and save again.", tone: "warn" } },
  { from: "save", box: { id: "offline", title: "No answer", detail: "\"Couldn't reach the server.\" Your edits stay in the form.", tone: "warn" } },
];

/** Records (quotes, invoices, shop items) check a version when you save. Settings do not. */
export const RECORD_FLOW: FlowBox[] = [
  { id: "rec-save", title: "Save a record", detail: "A quote, an invoice, or a shop item.", tone: "step" },
  { id: "rec-check", title: "Version check", detail: "Has someone else saved it since you opened it?", tone: "step" },
  { id: "rec-ok", title: "Saved", detail: "No: your change is stored.", tone: "ok" },
  { id: "rec-conflict", title: "Conflict", detail: "Yes: \"This was changed in another tab or by another user.\" Choose Reload or Overwrite.", tone: "warn" },
];
