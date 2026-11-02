import { describe, expect, it, vi } from "vitest";
import {
  emptyForm,
  formFromInvoice,
  formFromInvoiceDuplicate,
  newInvoiceDates,
  buildInput,
  previewTotalsFor,
  hasQuoteProvenance,
  matchingClientByName,
  defaultPaymentForm,
  emailDisabledReason,
  formatSentAt,
  linkLineToItem,
  unlinkLine,
  type InvoiceForm,
  type LineForm,
} from "./invoiceForm";
import type { Invoice } from "./invoicesApi";
import type { Item } from "./itemsApi";
import type { Client } from "./clientsApi";

function makeInvoice(overrides: Partial<Invoice> = {}): Invoice {
  return {
    id: "inv-1",
    number: "INV-0001",
    status: "draft",
    clientId: "",
    clientName: "Walk-in",
    clientEmail: "",
    clientBillingAddress: "",
    clientShippingAddress: "",
    issueDate: "2026-01-01",
    dueDate: "2026-01-15",
    taxBp: 725,
    discountCents: 100,
    notes: "notes",
    subtotalCents: 5000,
    taxCents: 355,
    totalCents: 5255,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    lines: [
      { id: "l1", description: "Bracket", quantity: 2, unitPriceCents: 2500, discountCents: 0, quoteId: "", kind: "item", itemId: "", optionName: "", stockTaken: 0, amountCents: 5000 },
    ],
    payments: [],
    paidCents: 855,
    balanceCents: 4000,
    sends: [],
    createdBy: "",
    updatedBy: "",
    ...overrides,
  };
}

describe("emptyForm", () => {
  it("starts blank with the configured default tax pct and no lines", () => {
    expect(emptyForm(7.25)).toEqual({
      clientId: "",
      clientName: "",
      issueDate: "",
      dueDate: "",
      taxPct: 7.25,
      discount: 0,
      notes: "",
      lines: [],
    });
  });

  it("prefills issueDate/dueDate when a dates param is given (E5/E6's new-draft composer)", () => {
    const form = emptyForm(7.25, { issueDate: "2026-03-01", dueDate: "2026-03-31" });
    expect(form.issueDate).toBe("2026-03-01");
    expect(form.dueDate).toBe("2026-03-31");
  });
});

describe("newInvoiceDates", () => {
  it("defaults dueDate to issueDate + dueDays (E5/E6, mirrors server's fillSentDates)", () => {
    expect(newInvoiceDates(30, "2026-03-01")).toEqual({ issueDate: "2026-03-01", dueDate: "2026-03-31" });
  });

  it("carries a month/year rollover correctly", () => {
    expect(newInvoiceDates(10, "2026-12-28")).toEqual({ issueDate: "2026-12-28", dueDate: "2027-01-07" });
  });

  it("zero dueDays makes dueDate equal issueDate", () => {
    expect(newInvoiceDates(0, "2026-03-01")).toEqual({ issueDate: "2026-03-01", dueDate: "2026-03-01" });
  });

  it("defaults issueDate to today (local date, not UTC) when not given", () => {
    const today = new Date().toLocaleDateString("en-CA");
    expect(newInvoiceDates(30).issueDate).toBe(today);
  });

  // The default must be the caller's LOCAL date, not UTC's: 09:00 on the 15th in Samoa (UTC+13) is the 15th.
  it("defaults issueDate to the local date, not the UTC date, at a UTC+ boundary", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Pacific/Apia"; // UTC+13
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-14T20:00:00Z")); // 2026-01-15 09:00 local
      expect(newInvoiceDates(30).issueDate).toBe("2026-01-15");
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });

    // At a UTC- boundary, 18:00 local on the 14th (UTC-7) is the 15th in UTC; the default is still the 14th.
  it("defaults issueDate to the local date, not the UTC date, at a UTC- boundary", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Etc/GMT+7"; // UTC-7 (POSIX TZ names invert the sign)
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-15T01:00:00Z")); // 2026-01-14 18:00 local
      expect(newInvoiceDates(30).issueDate).toBe("2026-01-14");
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });
});

describe("formFromInvoice / buildInput round-trip", () => {
  it("converts cents/basis-points to dollars/percent and back losslessly", () => {
    const inv = makeInvoice();
    const form = formFromInvoice(inv);
    expect(form.taxPct).toBe(7.25);
    expect(form.discount).toBe(1);
    expect(form.lines).toEqual([{ description: "Bracket", quantity: 2, unitPrice: 25, discount: 0, quoteId: "", kind: "item", itemId: "", optionName: "" }]);

    const input = buildInput(form);
    expect(input.taxBp).toBe(725);
    expect(input.discountCents).toBe(100);
    expect(input.lines).toEqual([{ description: "Bracket", quantity: 2, unitPriceCents: 2500, discountCents: 0, quoteId: "", kind: "item", itemId: "", optionName: "" }]);
    expect(input.clientId).toBe("");
    expect(input.clientName).toBe("Walk-in"); // freeform client name preserved when clientId is empty
  });

  it("clears clientName from the form when the invoice is linked to a client", () => {
    const inv = makeInvoice({ clientId: "client-1", clientName: "Acme" });
    const form = formFromInvoice(inv);
    expect(form.clientId).toBe("client-1");
    expect(form.clientName).toBe(""); // linked clients don't carry a freeform name in the form

    const input = buildInput(form);
    expect(input.clientId).toBe("client-1");
    expect(input.clientName).toBe(""); // buildInput never sends a freeform name alongside a linked clientId
  });
});

describe("formFromInvoiceDuplicate", () => {
  it("copies client, lines, invoice-level discount/tax, and notes but blanks id/status/dates/quoteId", () => {
    const inv = makeInvoice({ clientId: "client-1", clientName: "Acme" });
    const form = formFromInvoiceDuplicate(inv);
    expect(form.clientId).toBe("client-1");
    expect(form.clientName).toBe(""); // linked client: no freeform name carried, same rule as formFromInvoice
    expect(form.taxPct).toBe(7.25);
    expect(form.discount).toBe(1);
    expect(form.notes).toBe("notes");
    expect(form.issueDate).toBe(""); // fresh per E5/E6, filled by the caller via newInvoiceDates()
    expect(form.dueDate).toBe("");
    expect(form.lines).toEqual([{ description: "Bracket", quantity: 2, unitPrice: 25, discount: 0, quoteId: "", kind: "item", itemId: "", optionName: "" }]);
  });

  it("drops a line's quoteId even when the source line carries one (a duplicate isn't itself quote-provenance)", () => {
    const inv = makeInvoice({
      lines: [{ id: "l1", description: "Bracket", quantity: 2, unitPriceCents: 2500, discountCents: 0, quoteId: "quote-9", kind: "item", itemId: "", optionName: "", stockTaken: 0, amountCents: 5000 }],
    });
    const form = formFromInvoiceDuplicate(inv);
    expect(form.lines).toEqual([{ description: "Bracket", quantity: 2, unitPrice: 25, discount: 0, quoteId: "", kind: "item", itemId: "", optionName: "" }]);
    expect(hasQuoteProvenance(form)).toBe(false);
  });

  it("preserves a Shipping line's description verbatim so the server's converted-shipping tax exclusion still applies", () => {
    const inv = makeInvoice({
      taxBp: 1000,
      lines: [
        { id: "l1", description: "Bracket", quantity: 2, unitPriceCents: 2500, discountCents: 0, quoteId: "", kind: "item", itemId: "", optionName: "", stockTaken: 0, amountCents: 5000 },
        { id: "l2", description: "Shipping", quantity: 1, unitPriceCents: 800, discountCents: 0, quoteId: "", kind: "shipping", itemId: "", optionName: "", stockTaken: 0, amountCents: 800 },
      ],
    });
    const form = formFromInvoiceDuplicate(inv);
    expect(form.taxPct).toBe(10);
    expect(form.lines.map((l) => l.description)).toEqual(["Bracket", "Shipping"]);
  });

  it("preserves the freeform clientName for a walk-in (no clientId) invoice", () => {
    const inv = makeInvoice({ clientId: "", clientName: "Walk-in Jane" });
    expect(formFromInvoiceDuplicate(inv).clientName).toBe("Walk-in Jane");
  });
});

describe("buildInput", () => {
  const base: InvoiceForm = {
    clientId: "",
    clientName: "Walk-in",
    issueDate: "2026-01-01",
    dueDate: "2026-01-15",
    taxPct: 10,
    discount: 2.5,
    notes: "n",
    lines: [{ description: "d", quantity: 3, unitPrice: 12.34, discount: 0.5, quoteId: "q1", kind: "item", itemId: "", optionName: "" }],
  };

  it("converts dollar/percent form fields to integer cents/basis points", () => {
    const input = buildInput(base);
    expect(input.taxBp).toBe(1000);
    expect(input.discountCents).toBe(250);
    expect(input.lines[0]).toEqual({ description: "d", quantity: 3, unitPriceCents: 1234, discountCents: 50, quoteId: "q1", kind: "item", itemId: "", optionName: "" });
  });

  it("drops the freeform clientName once a clientId is present", () => {
    const input = buildInput({ ...base, clientId: "client-9" });
    expect(input.clientId).toBe("client-9");
    expect(input.clientName).toBe("");
  });
});

describe("previewTotalsFor", () => {
  it("matches the engine's invoiceTotals for the same inputs", () => {
    const form: InvoiceForm = {
      clientId: "",
      clientName: "",
      issueDate: "",
      dueDate: "",
      taxPct: 10,
      discount: 0,
      notes: "",
      lines: [
        { description: "a", quantity: 2, unitPrice: 10, discount: 0, quoteId: "", kind: "item", itemId: "", optionName: "" },
        { description: "b", quantity: 1, unitPrice: 5, discount: 1, quoteId: "", kind: "item", itemId: "", optionName: "" },
      ],
    };
    const totals = previewTotalsFor(form);
    expect(totals.subtotalCents).toBe(2400);
    expect(totals.taxCents).toBe(240);
    expect(totals.totalCents).toBe(2640);
  });

  it("exempts a shipping-kind line from tax, matching convertQuote.ts's convertPreview for the same inputs", () => {
    // Mirrors convertPreview's "taxes only the product line" case (2 x 1250c plus 1000c Shipping, 8% tax):
    // previewTotalsFor must match the server's stored subtotal/tax/total.
    const form: InvoiceForm = {
      clientId: "",
      clientName: "Walk-in",
      issueDate: "",
      dueDate: "",
      taxPct: 8,
      discount: 0,
      notes: "",
      lines: [
        { description: "benchy for Sam", quantity: 2, unitPrice: 12.5, discount: 0, quoteId: "q1", kind: "item", itemId: "", optionName: "" },
        { description: "Shipping", quantity: 1, unitPrice: 10, discount: 0, quoteId: "q1", kind: "shipping", itemId: "", optionName: "" },
      ],
    };
    const totals = previewTotalsFor(form);
    expect(totals.subtotalCents).toBe(3500);
    expect(totals.taxCents).toBe(200);
    expect(totals.totalCents).toBe(3700);
  });

  it("taxes a line described Shipping when its kind is item, and exempts any shipping-kind line", () => {
    const base: InvoiceForm = { ...emptyForm(10), clientName: "X" };
    const line = (description: string, kind: "item" | "shipping"): LineForm =>
      ({ description, quantity: 1, unitPrice: 10, discount: 0, quoteId: "", kind, itemId: "", optionName: "" });
    expect(previewTotalsFor({ ...base, lines: [line("Shipping", "item")] }).taxCents).toBe(100);
    expect(previewTotalsFor({ ...base, lines: [line("Postage", "shipping")] }).taxCents).toBe(0);
  });

  it("buildInput sends each line's kind", () => {
    const form: InvoiceForm = {
      ...emptyForm(0),
      clientName: "X",
      lines: [{ description: "Postage", quantity: 1, unitPrice: 5, discount: 0, quoteId: "", kind: "shipping", itemId: "", optionName: "" }],
    };
    expect(buildInput(form).lines[0]?.kind).toBe("shipping");
  });

  it("formFromInvoice -> previewTotalsFor reproduces a stored converted invoice's totals exactly (opening it for edit must not silently change the total)", () => {
    const inv = makeInvoice({
      taxBp: 800,
      discountCents: 0,
      subtotalCents: 3500,
      taxCents: 200,
      totalCents: 3700,
      lines: [
        { id: "l1", description: "benchy for Sam", quantity: 2, unitPriceCents: 1250, discountCents: 0, quoteId: "q1", kind: "item", itemId: "", optionName: "", stockTaken: 0, amountCents: 2500 },
        { id: "l2", description: "Shipping", quantity: 1, unitPriceCents: 1000, discountCents: 0, quoteId: "q1", kind: "shipping", itemId: "", optionName: "", stockTaken: 0, amountCents: 1000 },
      ],
    });
    const totals = previewTotalsFor(formFromInvoice(inv));
    expect(totals.subtotalCents).toBe(inv.subtotalCents);
    expect(totals.taxCents).toBe(inv.taxCents);
    expect(totals.totalCents).toBe(inv.totalCents);
  });
});

describe("hasQuoteProvenance", () => {
  it("false when no line carries a quoteId", () => {
    expect(hasQuoteProvenance({ lines: [{ description: "", quantity: 1, unitPrice: 0, discount: 0, quoteId: "", kind: "item", itemId: "", optionName: "" }] })).toBe(false);
  });
  it("true once any line carries a quoteId", () => {
    expect(
      hasQuoteProvenance({
        lines: [
          { description: "", quantity: 1, unitPrice: 0, discount: 0, quoteId: "", kind: "item", itemId: "", optionName: "" },
          { description: "", quantity: 1, unitPrice: 0, discount: 0, quoteId: "quote-1", kind: "item", itemId: "", optionName: "" },
        ],
      }),
    ).toBe(true);
  });
  it("false for an empty line list", () => {
    expect(hasQuoteProvenance({ lines: [] })).toBe(false);
  });
});

describe("matchingClientByName", () => {
  const clients: Client[] = [
    {
      id: "c1",
      name: "Acme Co",
      email: "",
      phone: "",
      billingAddress: "",
      shippingAddress: "",
      notes: "",
      createdAt: "",
      updatedAt: "",
      createdBy: "",
      updatedBy: "",
    },
  ];

  it("null once a clientId is already linked", () => {
    expect(matchingClientByName(clients, { clientId: "c1", clientName: "Acme Co" })).toBeNull();
  });
  it("null when the typed name is blank", () => {
    expect(matchingClientByName(clients, { clientId: "", clientName: "   " })).toBeNull();
  });
  it("finds a case-insensitive, trimmed match on the freeform name", () => {
    expect(matchingClientByName(clients, { clientId: "", clientName: "  acme co  " })).toEqual(clients[0]);
  });
  it("null when no client matches", () => {
    expect(matchingClientByName(clients, { clientId: "", clientName: "Nobody" })).toBeNull();
  });
});

describe("defaultPaymentForm", () => {
  it("defaults amount to the invoice balance in dollars and method to the configured default", () => {
    const form = defaultPaymentForm(4000, "Cash", "2026-01-20");
    expect(form).toEqual({ paidDate: "2026-01-20", amount: 40, method: "Cash", note: "", reference: "" });
  });
  it("defaults to a zero amount when there is no balance (no invoice yet)", () => {
    expect(defaultPaymentForm(0, "Cash", "2026-01-20").amount).toBe(0);
  });
  it("defaults paidDate to today (local date, not UTC) when not given", () => {
    const today = new Date().toLocaleDateString("en-CA");
    expect(defaultPaymentForm(0, "").paidDate).toBe(today);
  });

  // A payment recorded in the evening in the Americas must default to that local date, not the UTC date.
  it("defaults paidDate to the local date, not the UTC date, at a UTC- boundary", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Etc/GMT+7"; // UTC-7
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-15T01:00:00Z")); // 2026-01-14 18:00 local
      expect(defaultPaymentForm(0, "").paidDate).toBe("2026-01-14");
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });

  it("defaults paidDate to the local date, not the UTC date, at a UTC+ boundary", () => {
    const originalTz = process.env.TZ;
    process.env.TZ = "Pacific/Apia"; // UTC+13
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date("2026-01-14T20:00:00Z")); // 2026-01-15 09:00 local
      expect(defaultPaymentForm(0, "").paidDate).toBe("2026-01-15");
    } finally {
      vi.useRealTimers();
      process.env.TZ = originalTz;
    }
  });
});

describe("emailDisabledReason", () => {
  it("\"unconfigured\" when smtp host is blank, regardless of client email", () => {
    expect(emailDisabledReason("", makeInvoice({ clientEmail: "a@b.com" }))).toBe("unconfigured");
    expect(emailDisabledReason("", makeInvoice({ clientEmail: "" }))).toBe("unconfigured");
  });
  it("\"no-email\" when smtp is configured but the invoice has no client email", () => {
    expect(emailDisabledReason("smtp.example.com", makeInvoice({ clientEmail: "" }))).toBe("no-email");
  });
  it("null (enabled) when smtp is configured and the invoice has a client email", () => {
    expect(emailDisabledReason("smtp.example.com", makeInvoice({ clientEmail: "a@b.com" }))).toBeNull();
  });
});

import { formatDateTime } from "./format";

describe("formatSentAt", () => {
  it("formats a valid RFC3339 timestamp as an en-US date and time", () => {
    const iso = "2026-01-15T10:30:00Z";
    expect(formatSentAt(iso)).toBe(formatDateTime(iso));
  });
  it("passes through an unparseable value unchanged", () => {
    expect(formatSentAt("not-a-date")).toBe("not-a-date");
  });
});

describe("item links on lines", () => {
  const item: Item = {
    id: "it1",
    name: "Dragon",
    description: "",
    priceCents: 1500,
    optionLabel: "Color",
    options: [
      { name: "Red", deltaCents: 0 },
      { name: "Gold", deltaCents: 250 },
    ],
    imageFile: "",
    public: false,
    stockMode: "in_stock",
    stockQty: 3,
    allowBackorder: false,
    sortOrder: 0,
    createdAt: "",
    updatedAt: "",
    createdBy: "",
    updatedBy: "",
  };
  const blank: LineForm = { description: "", quantity: 1.5, unitPrice: 0, discount: 2, quoteId: "", kind: "item", itemId: "", optionName: "" };

  it("fills description, price plus option delta, and the link, keeping the line discount", () => {
    expect(linkLineToItem(blank, item, "Gold")).toEqual({
      description: "Dragon (Gold)",
      quantity: 2,
      unitPrice: 17.5,
      discount: 2,
      quoteId: "",
      kind: "item",
      itemId: "it1",
      optionName: "Gold",
    });
  });

  it("links an item without options by its plain name", () => {
    const plain = linkLineToItem(blank, { ...item, options: [] }, "");
    expect(plain.description).toBe("Dragon");
    expect(plain.unitPrice).toBe(15);
    expect(plain.optionName).toBe("");
  });

  it("unlink clears the link and keeps what was typed", () => {
    const linked = { ...linkLineToItem(blank, item, "Red"), unitPrice: 12 };
    expect(unlinkLine(linked)).toMatchObject({ description: "Dragon (Red)", unitPrice: 12, itemId: "", optionName: "" });
  });

  it("round-trips the link through the form and the API input, and a duplicate keeps it", () => {
    const inv = makeInvoice({
      lines: [{ id: "l1", description: "Dragon (Red)", quantity: 1, unitPriceCents: 1500, discountCents: 0, quoteId: "", kind: "item", itemId: "it1", optionName: "Red", stockTaken: 1, amountCents: 1500 }],
    });
    for (const form of [formFromInvoice(inv), formFromInvoiceDuplicate(inv)]) {
      const line = buildInput(form).lines[0];
      expect(line).toMatchObject({ itemId: "it1", optionName: "Red" });
      expect(line).not.toHaveProperty("stockTaken");
    }
  });
});
