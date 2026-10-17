import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { BillingSection } from "./Billing";
import { ExecutionSection } from "./Execution";

const value = { lowBalanceUsdMicros: 1_234_567, maxCallSpendUsdMicros: 2_345_678,
  limits: { minCallSpendUsdMicros: 50_001, maxCallSpendUsdMicros: 900_000, maxLowBalanceUsdMicros: Number.MAX_SAFE_INTEGER } };
afterEach(cleanup);
it("round trips an unchanged fractional threshold and only submits its field", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<BillingSection value={value} save={save} disabled={false} />);
  expect((screen.getByLabelText("Low-balance threshold (USD)") as HTMLInputElement).value).toBe("1.234567");
  fireEvent.click(screen.getByRole("button", { name: "Save balance warning" }));
  await waitFor(() => expect(save).toHaveBeenCalledWith({ lowBalanceUsdMicros: 1_234_567 }));
  fireEvent.change(screen.getByLabelText("Low-balance threshold (USD)"), { target: { value: "0" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Save balance warning" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Save balance warning" }));
  await waitFor(() => expect(save).toHaveBeenLastCalledWith({ lowBalanceUsdMicros: 0 }));
});
it("uses server bounds and distinguishes clearing from an omitted ceiling", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<ExecutionSection value={value} save={save} disabled={false} />);
  const input = screen.getByLabelText("Maximum per call (USD)");
  const button = screen.getByRole("button", { name: "Save per-call limit" }) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  fireEvent.change(input, { target: { value: "0.05" } });
  expect(button.disabled).toBe(true);
  fireEvent.change(input, { target: { value: "0.050001" } });
  expect(button.disabled).toBe(false);
  fireEvent.click(button);
  await waitFor(() => expect(screen.getByText("Per-call limit saved.")).toBeTruthy());
  expect(save).toHaveBeenCalledWith({ maxCallSpendUsdMicros: 50_001 });
  fireEvent.click(screen.getByLabelText("Limit each paid call"));
  fireEvent.click(button);
  await waitFor(() => expect(save).toHaveBeenLastCalledWith({ maxCallSpendUsdMicros: null }));
});
it("retains input and reports failed saves", async () => {
  render(<BillingSection value={value} save={vi.fn().mockRejectedValue(new Error("offline"))} disabled={false} />);
  fireEvent.click(screen.getByRole("button", { name: "Save balance warning" }));
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("offline"));
  expect((screen.getByLabelText("Low-balance threshold (USD)") as HTMLInputElement).value).toBe("1.234567");
});

