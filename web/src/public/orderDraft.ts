import { clearConfirmation, loadConfirmation, saveConfirmation } from "./confirmationStorage";

// What the visitor had typed when the server refused a submit because the shop had just closed. It lives in
// sessionStorage (this tab only, gone with the tab) so the form comes back when the shop reopens and the page
// is reloaded. A successful submit clears it.
const KEY = "draft";

export type OrderDraft = {
  kind: "order";
  itemId: string;
  selected: Record<string, string>;
  quantity: number | null;
  note: string;
  name: string;
  email: string;
};
export type EstimateDraft = {
  kind: "estimate";
  materialName: string;
  quantity: number | null;
  note: string;
  name: string;
  email: string;
};
export type Draft = OrderDraft | EstimateDraft;

export function saveDraft(draft: Draft): void {
  saveConfirmation(KEY, draft);
}

export function loadDraft(): Draft | null {
  const d = loadConfirmation<Draft>(KEY);
  if (!d || typeof d !== "object") return null;
  if (d.kind !== "order" && d.kind !== "estimate") return null;
  return d;
}

export function clearDraft(): void {
  clearConfirmation(KEY);
}

/** The notice the Closed page shows above its heading when a submit was refused because the shop closed. */
export function refusedNotice(kind: Draft["kind"]): string {
  return kind === "order"
    ? "Your order wasn't placed. The shop closed while you were ordering. What you typed is saved in this tab for when the shop reopens."
    : "Your estimate request wasn't sent. The shop closed while you were filling it in. What you typed is saved in this tab for when the shop reopens.";
}
