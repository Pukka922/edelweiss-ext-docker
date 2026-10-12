import { describe, expect, it } from "vitest";
import de from "../public/locales/de.json";
import en from "../public/locales/en.json";

/** The dotted keys of the texts of a locale file ("form.policies.no"), the way the SDK flattens them. */
const keys = (tree: object, prefix = ""): string[] =>
  Object.entries(tree).flatMap(([key, value]) =>
    typeof value === "object" && value !== null ? keys(value, `${prefix}${key}.`) : [`${prefix}${key}`],
  );

describe("locales", () => {
  it("have the same keys in English and German", () => {
    const [english, german] = [keys(en), keys(de)];
    expect(english.length).toBeGreaterThan(0);
    expect(english.filter((key) => !german.includes(key))).toEqual([]); // missing in de.json
    expect(german.filter((key) => !english.includes(key))).toEqual([]); // missing in en.json
  });
});
