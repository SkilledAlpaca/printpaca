import { errorsFrom } from "./clientsApi";
import { DEFAULT_FETCH_TIMEOUT_MS } from "./fetchTimeouts";

/** The JSON shape of every /api/categories endpoint (server/categories.go's Category). `itemCount` counts all
 * items in the category, public or not. */
export interface Category {
  id: string;
  name: string;
  slug: string;
  sortOrder: number;
  hidden: boolean;
  itemCount: number;
  createdAt: string;
  updatedAt: string;
}

/** The POST/PUT body. An empty slug derives one from the name on POST and keeps the stored slug on PUT. */
export interface CategoryInput {
  name: string;
  slug: string;
  sortOrder: number;
  hidden: boolean;
}

/** A network failure returns null so the page can show a note and carry on without categories. */
export async function listCategories(): Promise<Category[] | null> {
  try {
    const res = await fetch("api/categories", { signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS) });
    if (!res.ok) return null;
    const body = (await res.json()) as { categories?: unknown };
    return Array.isArray(body.categories) ? (body.categories as Category[]) : [];
  } catch {
    return null;
  }
}

export async function createCategory(input: CategoryInput): Promise<Category> {
  const res = await fetch("api/categories", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Category;
}

export async function updateCategory(id: string, input: CategoryInput): Promise<Category> {
  const res = await fetch(`api/categories/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
  return (await res.json()) as Category;
}

/** Deletes the category. The server unlinks its items in the same transaction, leaving them with no category. */
export async function deleteCategory(id: string): Promise<void> {
  const res = await fetch(`api/categories/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal: AbortSignal.timeout(DEFAULT_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) throw await errorsFrom(res);
}
