// Copy for the Quote tab's per-card "?" help (CardHelp.svelte) and the Guide page's Concepts section
// (GettingStarted.svelte).

export const TILE_IDS = ["gcode", "print", "costs", "breakdown", "pricing", "history"] as const;
export type TileId = (typeof TILE_IDS)[number];

export const TILE_HELP: Record<TileId, string> = {
  gcode: "Drop a sliced .gcode or .3mf file, or an STL or 3MF model. printpaca reads the slicer's weight and time, or estimates weight from the model, and fills in the Print box.",
  print: "The job itself: printer, material, model weight, print time, and quantity. Waste adds a percentage to the model weight to cover purge and supports. With a time-of-use schedule, it also holds the print start, because the start decides which rates the print pays.",
  costs: "Your shop rates: electricity, labor, printer depreciation, and overhead. These usually stay the same between quotes; set defaults on the Electricity and Printers pages.",
  breakdown: "Live cost breakdown per unit and per batch. A dash means a required input above is missing or invalid.",
  pricing: "Turns a cost into a price: margin, marketplace fees, shipping, and tax. Shows the recommended price and net profit.",
  history: "Save the current quote to the server, and open any of your 5 most recent saves. See every saved quote, filter, export, import, convert to an invoice, or track its status on the Quotes page.",
};

// Guide "Concepts" topics, one h3/p pair each; CardHelp's guideAnchor links point at a topic's id.
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
];
