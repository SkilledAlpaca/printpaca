/** Shared AbortSignal.timeout budgets for the app's own fetches; the named exceptions below cover work
 * slower than a JSON round trip. */
export const DEFAULT_FETCH_TIMEOUT_MS = 10000;

/** sendApi.ts's outbound-email endpoints: dispatching through SMTP can be slow. */
export const EMAIL_SEND_TIMEOUT_MS = 45000;

/** itemsApi.ts's uploadItemImage: a multipart body takes longer to transfer and process. */
export const IMAGE_UPLOAD_TIMEOUT_MS = 30000;

/** requestsApi.ts's fetchRequestGcode: the file can reach MAX_MODEL_BYTES (64 MiB, gcodeMessage.ts). */
export const GCODE_DOWNLOAD_TIMEOUT_MS = 60000;
