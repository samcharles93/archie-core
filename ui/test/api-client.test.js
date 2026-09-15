import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import "./shim.js";
import { api, classifyActionError } from "../src/base/api.js";

// archied's mutation handlers run authorizeTaskMutation before doing anything
// else, and it refuses a request that does not declare
// Content-Type: application/json with 415. The dashboard must therefore send
// that header on every mutation -- including the ones with no body. Three
// bodyless mutations (bindingApprove, mappingDelete, bindingDelete) did not,
// and were refused 415 on every click.
const JSON_CONTENT_TYPE = "application/json";

// Every mutating method, with arguments that satisfy its signature. The
// inventories below are checked for completeness, so a new api method cannot
// be added without these contract assertions applying to it.
const MUTATIONS = [
	["taskAction", [1, "retry"]],
	["workRequest", [{ title: "t" }]],
	["channelReload", ["c1"]],
	["configUpdate", [{ "a.b": 1 }]],
	["configRepoUpdate", ["owner", "name", "field", 1]],
	["configReset", ["key"]],
	["mappingCreate", [{}]],
	["mappingUpdate", [1, {}]],
	["mappingDelete", [1]],
	["mappingPreview", [1, {}]],
	["bindingCreate", [{}]],
	["bindingUpdate", [1, {}]],
	["bindingDelete", [1]],
	["bindingApprove", [1]],
	["chatCancel", ["session"]],
	["chatMessage", ["channel", "hello"]],
	["chatPersona", ["session", "name"]],
	["chatUpdateDefer", [{}]],
	["chatUpdateInstall", [{}]],
	["chatDangerousRequest", ["kind", {}]],
	["chatDangerousDecision", ["id", "approve"]],
];

// Reads must not carry a mutation marker, and must ask for JSON.
const READS = [
	["summary", []],
	["tasks", []],
	["taskMeta", []],
	["task", [1]],
	["setup", []],
	["capabilities", []],
	["workflows", []],
	["skills", []],
	["channels", []],
	["curators", []],
	["config", []],
	["version", []],
	["logs", [{}]],
	["taskLogs", [1, {}]],
	["captures", [10]],
	["mappings", []],
	["bindings", []],
	["memory", []],
	["chatSessions", []],
	["chatMessages", ["s"]],
	["chatTurns", ["s"]],
	["chatUpdate", []],
	["chatDangerous", []],
];

// The one streaming call: it cannot be parsed as JSON, so it is asserted
// separately rather than folded into either inventory above.
const STREAMS = ["chatStream"];

let calls = [];

function stubFetch(respond) {
	calls = [];
	globalThis.fetch = async (path, init = {}) => {
		calls.push({ path, init });
		return respond(path, init);
	};
}

function okResponse() {
	return {
		ok: true,
		status: 200,
		statusText: "OK",
		json: async () => ({}),
		text: async () => "",
	};
}

test.after(() => {
	delete globalThis.fetch;
});

test("every mutation satisfies archied's browser mutation contract", async () => {
	for (const [name, args] of MUTATIONS) {
		stubFetch(okResponse);
		await api[name](...args);

		assert.equal(calls.length, 1, `${name} made ${calls.length} requests, expected 1`);
		const { init } = calls[0];
		assert.ok(init.method && init.method !== "GET", `${name} must use a mutating method`);
		assert.equal(
			init.headers["Content-Type"],
			JSON_CONTENT_TYPE,
			`${name} must send Content-Type: ${JSON_CONTENT_TYPE}; archied answers 415 without it`,
		);
		assert.equal(init.headers["X-Archie-CSRF"], "1", `${name} must send X-Archie-CSRF`);
	}
});

test("every read asks for JSON and carries no mutation marker", async () => {
	for (const [name, args] of READS) {
		stubFetch(okResponse);
		await api[name](...args);

		assert.equal(calls.length, 1, `${name} made ${calls.length} requests, expected 1`);
		const { init } = calls[0];
		assert.equal(init.headers.Accept, JSON_CONTENT_TYPE, `${name} must accept JSON`);
		assert.equal(init.headers["X-Archie-CSRF"], undefined, `${name} must not send a mutation marker`);
	}
});

test("the chat stream posts its payload and asks for an event stream", async () => {
	stubFetch(okResponse);
	await api.chatStream({ text: "hi" }, {});

	assert.equal(calls.length, 1);
	const { path, init } = calls[0];
	assert.equal(path, "/api/chat/stream");
	assert.equal(init.method, "POST");
	assert.equal(init.headers.Accept, "text/event-stream");
	assert.equal(init.headers["Content-Type"], JSON_CONTENT_TYPE);
	assert.equal(init.body, JSON.stringify({ text: "hi" }));
});

test("a bodyless DELETE resolves on 204 without parsing a body", async () => {
	stubFetch(() => ({
		ok: true,
		status: 204,
		statusText: "No Content",
		json: async () => {
			throw new Error("a 204 has no body to parse");
		},
		text: async () => "",
	}));

	await api.mappingDelete(1);
	await api.bindingDelete(1);
});

test("a 415 keeps the server's explanation and reads as refused", async () => {
	stubFetch(() => ({
		ok: false,
		status: 415,
		statusText: "Unsupported Media Type",
		text: async () => "Content-Type must be application/json\n",
		json: async () => ({}),
	}));

	await assert.rejects(
		() => api.bindingApprove(1),
		(err) => {
			assert.equal(err.status, 415);
			assert.equal(err.message, "Content-Type must be application/json");
			assert.equal(classifyActionError(err).kind, "refused");
			return true;
		},
	);
});

test("every api method is classified by this suite", () => {
	const classified = new Set([
		...MUTATIONS.map(([name]) => name),
		...READS.map(([name]) => name),
		...STREAMS,
	]);
	const actual = Object.keys(api).filter((key) => typeof api[key] === "function");
	const unclassified = actual.filter((name) => !classified.has(name)).sort();

	assert.deepEqual(
		unclassified,
		[],
		"add each new api method to MUTATIONS, READS, or STREAMS so the contract assertions cover it",
	);
});

// The module comment claims this file is the single place that talks to
// archied. That claim is only true while there is exactly one fetch() call
// here; a second one added later would silently bypass the shared CSRF,
// timeout, and error handling -- which is how the three 415s arose.
//
// stripComments keeps this count measuring code rather than prose about code:
// a comment that mentions fetch() must not be able to fail the gate, and must
// not be able to hide a real call either.
function stripComments(source) {
	let out = "";
	let quote = null;
	for (let i = 0; i < source.length; ) {
		const c = source[i];
		const next = source[i + 1];
		if (quote) {
			out += c;
			if (c === "\\") {
				out += next ?? "";
				i += 2;
				continue;
			}
			if (c === quote) quote = null;
			i++;
			continue;
		}
		if (c === '"' || c === "'" || c === "`") {
			quote = c;
			out += c;
			i++;
			continue;
		}
		if (c === "/" && next === "/") {
			while (i < source.length && source[i] !== "\n") i++;
			continue;
		}
		if (c === "/" && next === "*") {
			i += 2;
			while (i < source.length && !(source[i] === "*" && source[i + 1] === "/")) i++;
			i += 2;
			continue;
		}
		out += c;
		i++;
	}
	return out;
}

test("api.jsx holds exactly one fetch call", () => {
	const source = readFileSync(fileURLToPath(new URL("../src/base/api.jsx", import.meta.url)), "utf8");
	const count = (stripComments(source).match(/\bfetch\s*\(/g) ?? []).length;

	// A count of zero means the stripper ate the call, not that the file is
	// clean; this check must never pass vacuously.
	assert.ok(count >= 1, "no fetch() call found in api.jsx; the extractor is broken, not the file");
	assert.equal(
		count,
		1,
		`api.jsx contains ${count} fetch() calls, expected exactly 1: every call must go through the shared send() so CSRF, timeout, and error shape stay in one place`,
	);
});
