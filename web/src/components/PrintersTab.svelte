<script lang="ts">
  import SettingsEditor from "./settings/SettingsEditor.svelte";
  import PrintersSettings from "./settings/PrintersSettings.svelte";
  import PresetsSettings from "./settings/PresetsSettings.svelte";
</script>

<div class="page">
  <h1>Printers</h1>

  <SettingsEditor>
    {#snippet children({ form, builtin, hasError })}
      <PrintersSettings {form} {hasError} />
      <PresetsSettings {form} {builtin} {hasError} />
    {/snippet}
  </SettingsEditor>
</div>
