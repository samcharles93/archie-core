import { test } from "node:test";
import assert from "node:assert/strict";
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

// Note: this suite asserts on the requests each method makes, never on the
// text of api.jsx. A source-scanning check ("api.jsx holds exactly one fetch
// call") was removed on purpose: it broke on a reformat and on a comment that
// merely mentioned fetch, so it tested the file's wording rather than the
// client's behaviour. Every method in the two inventories above is covered
// behaviourally instead.
