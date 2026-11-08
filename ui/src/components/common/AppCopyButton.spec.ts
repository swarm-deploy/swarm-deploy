import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import { copyText } from "../../utils/clipboard";
import AppCopyButton from "./AppCopyButton.vue";

vi.mock("../../utils/clipboard", () => ({ copyText: vi.fn().mockResolvedValue(undefined) }));

afterEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("AppCopyButton", () => {
  it("shows feedback after copying and restarts the timer on another click", async () => {
    vi.useFakeTimers();
    const wrapper = mount(AppCopyButton, { props: { value: "task-id", label: "Copy task ID" } });
    const button = wrapper.get("button");

    await button.trigger("click");
    expect(copyText).toHaveBeenCalledWith("task-id");
    expect(button.attributes("aria-label")).toBe("Copied");
    expect(button.find(".app-copy-button-check").exists()).toBe(true);

    await vi.advanceTimersByTimeAsync(1000);
    await button.trigger("click");
    await vi.advanceTimersByTimeAsync(600);
    expect(button.find(".app-copy-button-check").exists()).toBe(true);

    await vi.advanceTimersByTimeAsync(1000);
    expect(button.attributes("aria-label")).toBe("Copy task ID");
    expect(button.find(".app-copy-button-check").exists()).toBe(false);
    wrapper.unmount();
  });

  it("drops feedback from a copy that completes after the value changes", async () => {
    let finishCopy!: () => void;
    vi.mocked(copyText).mockImplementationOnce(() => new Promise((resolve) => { finishCopy = resolve; }));
    const wrapper = mount(AppCopyButton, { props: { value: "first", label: "Copy value" } });

    await wrapper.get("button").trigger("click");
    await wrapper.setProps({ value: "second" });
    finishCopy();
    await flushPromises();

    expect(wrapper.find(".app-copy-button-check").exists()).toBe(false);
    wrapper.unmount();
  });

  it("disables unavailable values while allowing an empty string to be copied", async () => {
    const wrapper = mount(AppCopyButton, { props: { value: null, label: "Copy value" } });
    const button = wrapper.get("button");

    expect(button.element.disabled).toBe(true);
    await button.trigger("click");
    expect(copyText).not.toHaveBeenCalled();

    await wrapper.setProps({ value: "" });
    await button.trigger("click");
    expect(copyText).toHaveBeenCalledExactlyOnceWith("");
    wrapper.unmount();
  });
});
