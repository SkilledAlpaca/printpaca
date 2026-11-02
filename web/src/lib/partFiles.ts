import { MAX_PART_LABEL_CHARS } from "./engine/index";

const MODEL_EXTENSION = /\.(gcode|gco|g|nc|stl|3mf)$/i;

/** A part label from a dropped file name: model extension removed (a sliced "bracket.gcode.3mf" keeps its
 * first extension), trimmed, capped at the label limit. Falls back to the raw name if stripping leaves
 * nothing. */
export function partLabelFromFile(fileName: string): string {
  const stripped = fileName.replace(MODEL_EXTENSION, "").trim();
  return (stripped === "" ? fileName.trim() : stripped).slice(0, MAX_PART_LABEL_CHARS);
}
