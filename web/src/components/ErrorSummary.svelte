<script lang="ts">
  import { resolveByLabel, type ErrorTarget } from "../lib/fieldErrors";

  // A form's error list (WCAG 3.3.1, 3.3.3). The alert wrapper is a plain div around a plain ul (a role on the ul
  // would remove its list semantics). Each error that names a control gets a button that focuses it, and the
  // control itself carries aria-invalid and an aria-describedby pointing at its message while the error shows.
  let {
    errors,
    resolve = resolveByLabel,
  }: {
    errors: string[];
    /** Finds the control (or fallback element) for one error; defaults to matching the "Label:" prefix. */
    resolve?: (message: string) => ErrorTarget | null;
  } = $props();

  const uid = $props.id();
  let targets = $state<(ErrorTarget | null)[]>([]);

  $effect(() => {
    // Runs after the list renders, so the form's controls are already in the DOM.
    const found = errors.map((e) => resolve(e));
    targets = found;
    const touched: { el: HTMLElement; id: string; hadInvalid: string | null }[] = [];
    found.forEach((t, i) => {
      if (!t?.field) return;
      const id = `${uid}-err-${i}`;
      const hadInvalid = t.el.getAttribute("aria-invalid");
      t.el.setAttribute("aria-invalid", "true");
      const tokens = (t.el.getAttribute("aria-describedby") ?? "").split(/\s+/).filter(Boolean);
      t.el.setAttribute("aria-describedby", [...tokens, id].join(" "));
      touched.push({ el: t.el, id, hadInvalid });
    });
    return () => {
      for (const { el, id, hadInvalid } of touched) {
        if (hadInvalid === null) el.removeAttribute("aria-invalid");
        const tokens = (el.getAttribute("aria-describedby") ?? "").split(/\s+/).filter((t) => t && t !== id);
        if (tokens.length > 0) el.setAttribute("aria-describedby", tokens.join(" "));
        else el.removeAttribute("aria-describedby");
      }
    };
  });
</script>

<!-- The alert container stays mounted and only its content changes, so assistive tech announces each new list. -->
<div role="alert">
  {#if errors.length > 0}
    <ul class="errors">
      {#each errors as e, i (e)}
        <li id={`${uid}-err-${i}`}>
          {#if targets[i]}
            <button type="button" class="error-link" onclick={() => targets[i]?.el.focus()}>{e}</button>
          {:else}
            {e}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .error-link {
    min-height: 0;
    padding: 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    text-decoration: underline;
  }
</style>
