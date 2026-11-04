<script lang="ts">
  // A text input with a filtered listbox (WAI-ARIA combobox pattern, list autocomplete). The value is free text:
  // typing filters `options`, ArrowDown and ArrowUp move through the matches, Enter picks the highlighted one,
  // Escape closes the list (a second Escape reaches whatever is behind it), and clicking an option picks it. A value
  // outside the list stays as typed; the caller validates it. The ids come from the surrounding Field.
  let {
    id,
    value = $bindable(""),
    options,
    filter,
    placeholder,
    describedby,
    invalid,
    label,
  }: {
    id: string;
    value?: string;
    options: readonly string[];
    /** Narrows `options` for the typed text. Defaults to a case-insensitive substring match. */
    filter?: (query: string, options: readonly string[]) => string[];
    placeholder?: string;
    describedby?: string;
    invalid?: "true";
    /** Accessible name of the list; the input is named by the Field's label. */
    label: string;
  } = $props();

  const listId = $derived(`${id}-listbox`);
  let open = $state(false);
  let active = $state(-1);
  // After a pick, or on first focus, show the whole list rather than only the current value's match.
  let browsing = $state(true);
  let listEl: HTMLElement | undefined = $state();

  const matches = $derived(
    browsing
      ? [...options]
      : filter
        ? filter(value, options)
        : options.filter((o) => o.toLowerCase().includes(value.trim().toLowerCase())),
  );

  function show() {
    open = true;
  }
  function close() {
    open = false;
    active = -1;
  }
  function pick(v: string) {
    value = v;
    browsing = true;
    close();
  }
  function move(delta: number) {
    if (!open) show();
    if (matches.length === 0) return;
    active = active < 0 ? (delta > 0 ? 0 : matches.length - 1) : (active + delta + matches.length) % matches.length;
  }

  $effect(() => {
    // Keep the highlighted option in view inside the scrolling list.
    if (!open || active < 0 || !listEl) return;
    listEl.children[active]?.scrollIntoView({ block: "nearest" });
  });

  function onkeydown(e: KeyboardEvent) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      move(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      move(-1);
    } else if (e.key === "Enter") {
      const chosen = open && active >= 0 ? matches[active] : undefined;
      if (chosen !== undefined) {
        e.preventDefault();
        pick(chosen);
      }
    } else if (e.key === "Escape") {
      if (open) {
        e.preventDefault();
        e.stopPropagation();
        close();
      }
    } else if (e.key === "Home" && open && active >= 0) {
      e.preventDefault();
      active = 0;
    } else if (e.key === "End" && open && active >= 0) {
      e.preventDefault();
      active = matches.length - 1;
    }
  }
</script>

<div class="combobox">
  <input
    {id}
    type="text"
    role="combobox"
    aria-expanded={open}
    aria-controls={listId}
    aria-autocomplete="list"
    aria-activedescendant={open && active >= 0 ? `${id}-opt-${active}` : undefined}
    aria-describedby={describedby}
    aria-invalid={invalid ?? "false"}
    autocomplete="off"
    autocapitalize="off"
    spellcheck="false"
    {placeholder}
    bind:value
    oninput={() => {
      browsing = false;
      active = matches.length > 0 ? 0 : -1;
      show();
    }}
    onfocus={() => (browsing = true)}
    onclick={show}
    onblur={close}
    {onkeydown}
  />
  <!-- The listbox stays in the DOM so aria-controls always resolves; it is hidden while closed. mousedown is
       prevented so a click on an option doesn't blur the input before the pick lands. -->
  <ul
    id={listId}
    class="combobox-list"
    role="listbox"
    aria-label={label}
    hidden={!open || matches.length === 0}
    bind:this={listEl}
    onmousedown={(e) => e.preventDefault()}
  >
    {#each open ? matches : [] as opt, i (opt)}
      <li
        id={`${id}-opt-${i}`}
        role="option"
        aria-selected={i === active}
        class:active={i === active}
        class:current={opt === value}
        onclick={() => pick(opt)}
        onkeydown={() => {}}
      >{opt}</li>
    {/each}
  </ul>
  <!-- Outside the listbox: a listbox with no options breaks the ARIA required-children rule. -->
  <div class="combobox-list combobox-empty" role="status">{open && matches.length === 0 ? "No matching option. A typed name is checked when you save." : ""}</div>
</div>

<style>
  .combobox { position: relative; display: block; min-width: 0; width: 100%; max-width: min(100%, var(--field-l)); }
  .combobox input { width: 100%; max-width: none; }
  .combobox-list {
    position: absolute;
    inset-inline: 0;
    top: calc(100% + var(--space-1));
    z-index: 20;
    max-height: calc(var(--control-h) * 6);
    overflow-y: auto;
    margin: 0;
    padding: var(--space-1);
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--control-border);
    border-radius: calc(var(--radius) / 2);
    box-shadow: var(--shadow);
  }
  .combobox-list[hidden] { display: none; }
  li[role="option"] {
    padding: var(--space-2) var(--space-3);
    border-radius: calc(var(--radius) / 2);
    cursor: pointer;
    font-size: var(--type-body);
    color: var(--text);
  }
  li[role="option"].current { font-weight: 700; }
  li[role="option"].active { background: var(--surface-2); outline: 2px solid var(--accent-text); outline-offset: -2px; }
  li[role="option"]:hover { background: var(--surface-2); }
  .combobox-empty:empty { display: none; }
  .combobox-empty { padding: var(--space-2) var(--space-3); color: var(--muted); font-size: var(--type-secondary); }
  @media (max-width: 600px) {
    li[role="option"] { min-height: 44px; display: flex; align-items: center; }
  }
</style>
