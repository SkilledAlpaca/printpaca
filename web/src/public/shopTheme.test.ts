import { describe, expect, it } from "vitest";
import { parseShopTheme, readShopThemeCookie, themeCookie } from "./shopTheme";

describe("shop theme cookie", () => {
  it("accepts only light and dark", () => {
    expect(parseShopTheme("light")).toBe("light");
    expect(parseShopTheme("dark")).toBe("dark");
    for (const v of ["", "Dark", "auto", "dark ", null, undefined, 1]) expect(parseShopTheme(v)).toBeNull();
  });

  it("reads its cookie among others", () => {
    expect(readShopThemeCookie("a=1; pp_theme=dark; b=2")).toBe("dark");
    expect(readShopThemeCookie("pp_theme=light")).toBe("light");
    expect(readShopThemeCookie("pp_theme=garbage")).toBeNull();
    expect(readShopThemeCookie("xpp_theme=dark")).toBeNull();
    expect(readShopThemeCookie("")).toBeNull();
  });

  it("writes a one-year Lax cookie on the whole site, never HttpOnly", () => {
    expect(themeCookie("dark", false)).toBe("pp_theme=dark; Max-Age=31536000; Path=/; SameSite=Lax");
    expect(themeCookie("light", true)).toContain("; Secure");
    expect(themeCookie("light", true)).not.toContain("HttpOnly");
  });
});
