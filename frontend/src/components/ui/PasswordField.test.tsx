import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { PasswordField } from "./PasswordField";

afterEach(cleanup);
it("keeps the password value and autocomplete when toggled", () => {
  render(<PasswordField id="test-password" label="New password" defaultValue="passphrase" autoComplete="new-password" />);
  const field = screen.getByLabelText("New password") as HTMLInputElement;
  expect(field.type).toBe("password");
  fireEvent.click(screen.getByRole("button", { name: "Show new password" }));
  expect(field.type).toBe("text");
  expect(field.value).toBe("passphrase");
  expect(field.autocomplete).toBe("new-password");
  fireEvent.click(screen.getByRole("button", { name: "Hide new password" }));
  expect(field.type).toBe("password");
});

