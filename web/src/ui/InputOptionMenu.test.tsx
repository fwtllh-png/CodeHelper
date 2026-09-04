import {cleanup, fireEvent, render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import {InputOptionMenu} from "./InputOptionMenu";

afterEach(cleanup);

describe("InputOptionMenu", () => {
  it("renders visible suggestions and selects a long option", () => {
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

    expect(screen.getByRole("region", {name: "Suggested answers"})).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {
      name: "Implement the complete deterministic core with validation"
    }));
    expect(onChange).toHaveBeenCalledWith(
      "Implement the complete deterministic core with validation"
    );
  });

  it("exposes the controlled selection to assistive technology", () => {
    const {rerender} = render(
      <InputOptionMenu
        value=""
        options={["First", "Second"]}
        onChange={vi.fn()}
      />
    );

    expect(screen.getByRole("button", {name: "First"}))
      .toHaveProperty("ariaPressed", "false");

    rerender(
      <InputOptionMenu
        value="Second"
        options={["First", "Second"]}
        onChange={vi.fn()}
      />
    );

    expect(screen.getByRole("button", {name: "Second"}))
      .toHaveProperty("ariaPressed", "true");
  });

  it("uses native focusable buttons", () => {
    const onChange = vi.fn();
    render(
      <InputOptionMenu
        value=""
        options={["First", "Second"]}
        onChange={onChange}
      />
    );

    const option = screen.getByRole("button", {name: "Second"});
    option.focus();
    expect(option).toBe(document.activeElement);
    fireEvent.click(option);
    expect(onChange).toHaveBeenCalledWith("Second");
  });
});
