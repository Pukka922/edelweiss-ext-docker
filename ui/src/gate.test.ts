import { ApiError, createI18n } from "@edelweiss/sdk";
import { describe, expect, it } from "vitest";
import en from "../public/locales/en.json";
import { errorText } from "./errors";
import { dockerErrors, noteDockerError } from "./gate";

describe("noteDockerError", () => {
  it("changes dockerErrors for docker_unavailable and docker_forbidden", () => {
    for (const code of ["docker_unavailable", "docker_forbidden"]) {
      const before = dockerErrors();
      noteDockerError(new ApiError(503, code, "x"));
      expect(dockerErrors()).toBe(before + 1);
    }
  });

  it("ignores every other error", () => {
    const before = dockerErrors();
    noteDockerError(new ApiError(502, "docker_error", "x"));
    noteDockerError(new ApiError(404, "not_found", "x"));
    noteDockerError(new ApiError(400, "invalid_request", "x", { field: "id" }));
    noteDockerError(new Error("docker_unavailable"));
    noteDockerError("docker_forbidden");
    noteDockerError(undefined);
    expect(dockerErrors()).toBe(before);
  });

  it("is called by errorText, which every page shows its errors through", () => {
    const i18n = createI18n("en");
    i18n.register("docker", "en", en);
    const t = (key: string, params?: Record<string, string | number>) =>
      i18n.translate("docker", key, params);
    const before = dockerErrors();
    errorText(new ApiError(503, "docker_unavailable", "gone"), t);
    expect(dockerErrors()).toBe(before + 1);
    errorText(new ApiError(404, "not_found", "gone"), t);
    expect(dockerErrors()).toBe(before + 1);
  });
});
