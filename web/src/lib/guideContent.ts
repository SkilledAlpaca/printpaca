// Copy for the Guide page: the Concepts section (GettingStarted.svelte).

// Guide "Concepts" topics, one h3/p pair each; a topic's id is its anchor (#/guide#<id>).
export interface GuideTopic {
  id: string;
  title: string;
  body: string;
}

export const GUIDE_TOPICS: GuideTopic[] = [
  {
    id: "configuring-printpaca",
    title: "Configuring printpaca",
    body: "You can edit materials, printers, electricity rates, labor presets, and fee schedules on the Printers, Filament, Electricity, and Settings pages. Edits stay local until you press Save changes in the save bar at the bottom of the page. The config.yaml file on the server seeds the starting values.",
  },
  {
    id: "model-files",
    title: "Model files and print time",
    body: "On New quote, drop a sliced .gcode or .3mf file, or an STL or 3MF model, on the model file box. printpaca reads the weight and print time from a sliced file, or estimates the weight from a model, and fills in the fields below. Several files at once make one part each. Files are read in your browser and never uploaded. A slicer is the program that turns a 3D model into instructions for the printer, for example Bambu Studio, PrusaSlicer, or Cura. It also estimates the print time. If a file has no time, open the model in your slicer, slice it, and type the time it shows.",
  },
  {
    id: "material-and-printer",
    title: "Material, printer, and print start",
    body: "Section 2 of New quote holds what you print on: the material, its price per kilogram, the waste percentage, the printer, and the quantity. Waste adds a percentage to the model weight to cover purge and supports. With a time-of-use electricity schedule, the section also holds the print start, because the start decides which rates the print pays. The shop rates (electricity, labor, printer wear, and overhead) sit under Costs and Edit rates. They usually stay the same between quotes, so set the defaults on the Electricity and Printers pages.",
  },
  {
    id: "cost-breakdown",
    title: "Cost breakdown",
    body: "The Breakdown in the summary shows your cost live, per unit and per batch. A dash means a required input is missing or invalid, and the summary names the first missing field with a link to it.",
  },
  {
    id: "saving-quotes",
    title: "Saving quotes",
    body: "Save keeps the current quote on the server, and Save as new keeps a copy. The Recent saves list opens any of your five most recent saves. The Quotes page lists every saved quote and lets you filter, export, import, convert a quote to an invoice, or track its status.",
  },
  {
    id: "margin-vs-markup",
    title: "Margin versus markup",
    body: "Margin sets the price before tax so profit is a share of that price: price = cost divided by one minus margin. Markup multiplies cost directly. A 50% margin and a 2× markup both double the cost, but the same number means different things in each mode. Tax is then added to the price, and marketplace fees are taken out of what remains, so net profit can be lower than the margin you picked. printpaca warns you when net profit goes negative.",
  },
  {
    id: "marketplace-fees",
    title: "Marketplace fees",
    body: "Named fee schedules (a percent of revenue plus fixed amounts) are defined on the Settings page. Pick one per quote in the Price and profit card to see those fees taken out of profit.",
  },
  {
    id: "waste-and-failure-buffers",
    title: "Waste and failure buffers",
    body: "Waste percent raises the material used per print to cover purge and supports. The failure rate spreads the cost of failed prints across the successful ones: at a 20% failure rate, you expect 0.25 failed attempts for every good print, so the buffer is 25% of the consumables cost. The maximum is 90%.",
  },
  {
    id: "batches-and-labor",
    title: "Batches and labor",
    body: "Quantity spreads prep labor and one-time costs across the batch. You see both the per-unit and batch totals.",
  },
  {
    id: "requests-finalize-vs-convert",
    title: "Requests: Finalize or Convert",
    body: "A request is something a customer sent through the shop: a custom estimate or a shop item order. Finalize means you have dealt with it. It takes the request off your to-do list, records the sale of a shop item, and takes the units out of that item's stock. Finalize does not email the customer, so reply to them yourself. Convert to invoice makes an invoice from the request and creates the client if that email is not on file. Converting also finalizes the request, marks it Invoiced, and links the request and the invoice both ways, so you do not bill the same request twice. A shop item sale and its stock are counted when you send the invoice, not when you convert, so a draft invoice you never send does not count as a sale. An estimate that has no print time yet cannot be converted until you enter the time from your slicer. Use Finalize for an order you fulfilled without invoicing, or Convert to bill the customer.",
  },
  {
    id: "shop-items-and-options",
    title: "Shop items and options",
    body: "Shop items are the products customers can order from your shop page. A new shop item starts hidden: turn on Public, or press Show in shop after you save, to make it visible. An option group is one choice the customer makes, such as Color or Size. Each group has a group name and a list of choices, and each choice can add or subtract money from the price. Leave Customer must choose one on to force an answer before ordering. Turn it off and the group starts on None. A shop item made to order has no fixed stock. An in-stock item counts down each time you finalize an order for it.",
  },
  {
    id: "spoolman",
    title: "Spoolman",
    body: "Spoolman is a separate program that tracks your filament spools. Once an admin adds its address under Integrations in Settings, the Filament page lists your spools, and New quote lets you pick a spool to fill the material cost. The Test button checks the address before or after you save it, including an address set by an environment variable. The material you choose on a quote still sets the density, so pick the matching material as well as the spool.",
  },
  {
    id: "users-and-permissions",
    title: "Users and permissions",
    body: "By default, anyone with the admin password has full access. An admin can switch on Multi-user mode in Settings and add staff accounts. Each account is granted whole pages, such as Quotes or Invoices. A staff member sees only the pages they were granted, and Settings shows them only their own password. Editing a quote needs the New quote grant, so a person who has only Quotes can read the list but not open a quote in the editor. If you see a page you can't use, ask an admin to grant it.",
  },
];

/** Keyboard shortcuts, shown in the Guide. Each works only on the pages named here; the sidebar footer lists
 * the ones that work on the page you are on. */
export const SHORTCUTS: { keys: string; action: string; where: string }[] = [
  { keys: "Ctrl+S (Cmd+S on Mac)", action: "Save", where: "New quote, invoice drafts, client details, Shop items (item editor), Settings, Filament materials, Electricity, Printers" },
  { keys: "Ctrl+K (Cmd+K on Mac)", action: "Jump to the page's filter box", where: "Quotes, Invoices, Clients, Requests, Shop items, Filament spools, Recent prints on New quote" },
  { keys: "Esc", action: "Close the open menu or the sidebar drawer", where: "Everywhere" },
];
