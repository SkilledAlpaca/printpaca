<script lang="ts">
  import SettingsEditor from "./settings/SettingsEditor.svelte";
  import PrintersSettings from "./settings/PrintersSettings.svelte";
  import PresetsSettings from "./settings/PresetsSettings.svelte";
  import PageHeader from "./ui/PageHeader.svelte";
</script>

<div class="page">
  <PageHeader title="Printers" />

  <SettingsEditor>
    {#snippet children({ form, builtin, hasError })}
      <PrintersSettings {form} {hasError} />
      <PresetsSettings {form} {builtin} {hasError} />
    {/snippet}
  </SettingsEditor>
</div>
