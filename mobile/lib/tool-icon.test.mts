import assert from "node:assert/strict";
import test from "node:test";

import { toolIcon } from "./tool-icon.ts";

function expectIcons(cases: Record<string, string>) {
  for (const [name, icon] of Object.entries(cases)) {
    assert.equal(toolIcon(name), icon, name);
  }
}

test("a todo list or a plan is a checklist, even when its name says write", () => {
  expectIcons({
    TodoWrite: "check-square",
    todo_write: "check-square",
    "Todo list": "check-square",
    update_plan: "check-square",
    ExitPlanMode: "check-square",
  });
});

test("deleting is a bin, not an edit", () => {
  expectIcons({
    Delete: "trash-2",
    delete_file: "trash-2",
    remove: "trash-2",
    "Remove file": "trash-2",
  });
});

test("a question is a question, and a task is not one", () => {
  expectIcons({
    AskUserQuestion: "help-circle",
    ask_question: "help-circle",
    ask: "help-circle",
    "Ask user": "help-circle",
    question: "help-circle",
    // "ask" sits inside each of these; none of them asks the user anything.
    Task: "git-branch",
    TaskCreate: "git-branch",
    subtask: "git-branch",
    multitask: "git-branch",
  });
});

test("artifacts and images get their own marks", () => {
  expectIcons({
    Artifact: "file-text",
    write_artifact: "file-text",
    GenerateImage: "image",
    generate_image: "image",
    view_image: "image",
  });
});

test("the existing families keep their marks", () => {
  expectIcons({
    Bash: "terminal",
    shell: "terminal",
    run_command: "terminal",
    Edit: "edit-3",
    Write: "edit-3",
    apply_patch: "edit-3",
    Read: "file-text",
    view_file: "file-text",
    Grep: "search",
    Glob: "search",
    WebFetch: "globe",
    webfetch: "globe",
    Agent: "git-branch",
    Mystery: "tool",
  });
});
