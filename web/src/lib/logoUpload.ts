/** Uploaded logos are stored in config.yaml as a `data:` URL in quote.logoUrl (no separate file store, so
 * backups and the demo carry them with the config). The public branding JSON repeats the value on every shop
 * page load and the settings PUT is capped at 256 KiB, so the file is capped well below that. */
export const LOGO_MAX_BYTES = 64 * 1024;
export const LOGO_ACCEPT = "image/png,image/jpeg,image/svg+xml,image/webp";
const LOGO_MAX_EDGE = 256;

const ACCEPTED = new Set(LOGO_ACCEPT.split(","));

/** The problem with a chosen file, or "" when it can be used as is. Rasters over the cap are downscaled
 * instead, so only the type and a vector over the cap are rejected here. */
export function logoFileProblem(type: string, size: number): string {
  if (!ACCEPTED.has(type)) return "Choose a PNG, JPG, WebP, or SVG image.";
  if (type === "image/svg+xml" && size > LOGO_MAX_BYTES) return `That SVG is larger than ${LOGO_MAX_BYTES / 1024} KB. Export a smaller one or use a PNG.`;
  return "";
}

/** "Uploaded image, 12 KB" for a stored data URL; "" for anything else. */
export function describeLogoDataUrl(url: string): string {
  if (!url.startsWith("data:")) return "";
  const comma = url.indexOf(",");
  const b64 = url.slice(comma + 1);
  const bytes = url.slice(0, comma).includes(";base64") ? Math.floor((b64.length * 3) / 4) : b64.length;
  return `Uploaded image, ${Math.max(1, Math.round(bytes / 1024))} KB`;
}

function readAsDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error);
    r.readAsDataURL(blob);
  });
}

/** Reads `file` as a data URL, downscaling a raster over the cap to at most 256 px on the long edge (PNG, so
 * transparency survives). Resolves `{ error }` when the file can't be used. DOM-bound, so untested. */
export async function logoToDataUrl(file: File): Promise<{ url: string } | { error: string }> {
  const problem = logoFileProblem(file.type, file.size);
  if (problem) return { error: problem };
  if (file.size <= LOGO_MAX_BYTES) return { url: await readAsDataUrl(file) };
  let bitmap: ImageBitmap | undefined;
  try {
    bitmap = await createImageBitmap(file);
    const scale = Math.min(1, LOGO_MAX_EDGE / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    canvas.getContext("2d")?.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
    if (!blob || blob.size > LOGO_MAX_BYTES) return { error: `That image is still larger than ${LOGO_MAX_BYTES / 1024} KB after shrinking. Use a simpler or smaller logo.` };
    return { url: await readAsDataUrl(blob) };
  } catch {
    return { error: "Couldn't read that image. Try a PNG or JPG." };
  } finally {
    bitmap?.close();
  }
}
