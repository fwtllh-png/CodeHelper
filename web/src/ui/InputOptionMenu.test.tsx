import {cleanup, fireEvent, render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import {InputOptionMenu} from "./InputOptionMenu";

afterEach(cleanup);

describe("InputOptionMenu", () => {
  it("renders a controlled listbox and selects long options", () => {
    const onChange = vi.fn();
    render(
      <InputOptionMenu
        value=""
        options={[
          "Implement the complete deterministic core with validation",
          "Review the design only"
        ]}
        onChange={onChange}
      />
    );

    expect(screen.queryByRole("combobox")).toBeNull();
    fireEvent.click(screen.getByRole("button", {name: "Input options"}));
    expect(screen.getByRole("listbox", {name: "Input options"})).toBeTruthy();
    fireEvent.click(screen.getByRole("option", {
      name: "Implement the complete deterministic core with validation"
    }));
    expect(onChange).toHaveBeenCalledWith(
      "Implement the complete deterministic core with validation"
    );
  });

  it("supports keyboard navigation and escape", () => {
    render(
      <InputOptionMenu
        value=""
        options={["First", "Second"]}
        onChange={vi.fn()}
      />
    );

    const trigger = screen.getByRole("button", {name: "Input options"});
    fireEvent.keyDown(trigger, {key: "ArrowDown"});
    fireEvent.keyDown(screen.getByRole("listbox"), {key: "End"});
    expect(screen.getByRole("option", {name: "Second"}))
      .toBe(document.activeElement);
    fireEvent.keyDown(screen.getByRole("listbox"), {key: "Escape"});
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(trigger).toBe(document.activeElement);
  });
});
