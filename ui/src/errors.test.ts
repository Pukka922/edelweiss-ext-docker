import { ApiError, createI18n } from "@edelweiss/sdk";
import { describe, expect, it } from "vitest";
import en from "../public/locales/en.json";
import { errorField, errorText } from "./errors";

const i18n = createI18n("en");
i18n.register("docker", "en", en);
const t = (key: string, params?: Record<string, string | number>) => i18n.translate("docker", key, params);

describe("errorText", () => {
  it("translates known codes with Docker's message, and shows unknown ones as they are", () => {
    expect(errorText(new ApiError(503, "docker_unavailable", "dial unix: no such file"), t)).toBe(
      "Docker is not running: dial unix: no such file",
    );
    expect(errorText(new ApiError(500, "something_new", "raw text"), t)).toBe("raw text");
    expect(errorText(new Error("plain"), t)).toBe("plain");
    expect(errorText("text", t)).toBe("text");
  });

  it("reads the field of an invalid input", () => {
    expect(errorField(new ApiError(400, "invalid_container_spec", "x", { field: "ports.0.hostPort" }))).toBe(
      "ports.0.hostPort",
    );
    expect(errorField(new ApiError(400, "x", "y"))).toBeUndefined();
    expect(errorField(new Error("z"))).toBeUndefined();
  });
});
