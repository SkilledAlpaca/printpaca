/** Which public view a path shows. The server serves public.html at both "/" (the front door) and "/order"
 * (the shop); everything else is the front door too, so an unexpected path never lands on a blank page. */
export type PublicView = "front-door" | "order";

export function publicViewFor(pathname: string): PublicView {
  return pathname === "/order" ? "order" : "front-door";
}
