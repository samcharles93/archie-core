import { test } from "node:test";
import assert from "node:assert/strict";
import "./shim.js";
import { ApiError, classifyActionError } from "../src/base/api.js";

test("classifyActionError treats 401 as an expired session", () => {
  assert.equal(classifyActionError(new ApiError("unauthorised", 401)).kind, "session-expired");
});

test("classifyActionError treats 400/403/409 as refused", () => {
  assert.equal(classifyActionError(new ApiError("bad id", 400)).kind, "refused");
  assert.equal(classifyActionError(new ApiError("cross-origin mutation refused", 403)).kind, "refused");
  assert.equal(classifyActionError(new ApiError("task changed state; refresh and try again", 409)).kind, "refused");
});

test("classifyActionError treats 5xx as broken", () => {
  assert.equal(classifyActionError(new ApiError("task action failed", 500)).kind, "broken");
  assert.equal(classifyActionError(new ApiError("runtime task control is unavailable", 503)).kind, "broken");
});

test("classifyActionError treats a statusless error (network/timeout) as broken", () => {
  assert.equal(classifyActionError(new TypeError("Failed to fetch")).kind, "broken");
  assert.equal(classifyActionError(new DOMException("The operation was aborted", "AbortError")).kind, "broken");
});

test("classifyActionError preserves the server's message", () => {
  assert.equal(classifyActionError(new ApiError("cross-origin mutation refused", 403)).message, "cross-origin mutation refused");
});
