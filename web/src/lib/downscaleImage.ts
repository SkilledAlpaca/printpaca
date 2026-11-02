const MAX_EDGE = 1600;
const SMALL_ENOUGH_BYTES = 1024 * 1024; // 1 MiB

/** Downscales to at most 1600px on the long edge as JPEG q0.85. Returns the original when it is already
 * small (<=1600px, <=1 MiB) or decoding fails; server/items.go's handleImageUpload still validates type
 * and the 5 MiB cap. Untested: DOM-bound (createImageBitmap, canvas). */
export async function downscaleImage(file: File): Promise<Blob> {
  let bitmap: ImageBitmap | undefined;
  try {
    bitmap = await createImageBitmap(file);
    const { width, height } = bitmap;
    if (width <= MAX_EDGE && height <= MAX_EDGE && file.size <= SMALL_ENOUGH_BYTES) {
      return file;
    }

    const scale = Math.min(1, MAX_EDGE / Math.max(width, height));
    const targetW = Math.max(1, Math.round(width * scale));
    const targetH = Math.max(1, Math.round(height * scale));

    const canvas = document.createElement("canvas");
    canvas.width = targetW;
    canvas.height = targetH;
    const ctx = canvas.getContext("2d");
    if (!ctx) return file;
    // JPEG has no alpha; fill white first so transparent sources don't get a black background.
    ctx.fillStyle = "#fff";
    ctx.fillRect(0, 0, targetW, targetH);
    ctx.drawImage(bitmap, 0, 0, targetW, targetH);

    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.85));
    return blob ?? file;
  } catch {
    return file;
  } finally {
    bitmap?.close();
  }
}
