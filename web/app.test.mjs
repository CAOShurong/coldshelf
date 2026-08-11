import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import vm from "node:vm";

const appSource = fs
  .readFileSync(new URL("./static/app.js", import.meta.url), "utf8")
  .split("\nbindEvents();")[0];

function createHarness(writeText) {
  let selectedNode = null;
  const toast = {
    textContent: "",
    classList: { add() {}, remove() {} },
  };
  const selection = {
    removeAllRanges() {},
    addRange(range) { selectedNode = range.node; },
  };
  const document = {
    querySelector(selector) {
      if (selector === "#toast") return toast;
      throw new Error(`Unexpected selector: ${selector}`);
    },
    querySelectorAll() { return []; },
    createRange() {
      return { selectNodeContents(node) { this.node = node; } };
    },
    createElement() {
      return {
        set textContent(value) { this.value = value; },
        get innerHTML() { return this.value; },
      };
    },
  };
  const context = {
    clearTimeout() {},
    document,
    fetch: async () => { throw new Error("Unexpected fetch"); },
    history: { replaceState() {} },
    Intl,
    location: { hash: "" },
    navigator: writeText ? { clipboard: { writeText } } : {},
    setTimeout() { return 1; },
    URLSearchParams,
    window: { getSelection: () => selection },
  };
  vm.createContext(context);
  vm.runInContext(appSource, context);
  return { context, selectedNode: () => selectedNode, toast };
}

test("copyPath copies special characters literally", async () => {
  let copied = "";
  const harness = createHarness(async (value) => { copied = value; });
  const pathNode = {};
  const path = '客户 "North <Draft>"/报价 "final" <signed>.pdf';

  await harness.context.copyPath(path, pathNode);

  assert.equal(copied, path);
  assert.equal(harness.toast.textContent, "Path copied.");
  assert.equal(harness.selectedNode(), null);
});

test("copyPath selects the visible path when permission is denied", async () => {
  const harness = createHarness(async () => { throw new Error("denied"); });
  const pathNode = {};

  await harness.context.copyPath("Documents/report.pdf", pathNode);

  assert.equal(harness.selectedNode(), pathNode);
  assert.equal(harness.toast.textContent, "Could not copy the path. It is selected for manual copying.");
});

test("copyPath selects the visible path when the API is unavailable", async () => {
  const harness = createHarness();
  const pathNode = {};

  await harness.context.copyPath("Documents/report.pdf", pathNode);

  assert.equal(harness.selectedNode(), pathNode);
  assert.equal(harness.toast.textContent, "Clipboard access is unavailable. Select the path and copy it manually.");
});
