import { describe, expect, it } from "vitest";
import { sparklinePoints } from "./Sparkline";

describe("sparklinePoints", () => {
  it("maps values onto the box (0 at the bottom, max at the top)", () => {
    expect(sparklinePoints([0, 50, 100], 120, 40)).toBe("0,40 60,20 120,0");
    expect(sparklinePoints([150, -5], 10, 10)).toBe("0,0 10,10");
    expect(sparklinePoints([], 120, 40)).toBe("");
    expect(sparklinePoints([25], 120, 40)).toBe("0,30");
  });
});
