"use client";
import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import type { Purchase, PurchaseStatus } from "@/lib/credits/types";
import type { PaymentMethod } from "@/components/checkout/types";
import { gstBreakdown } from "@/lib/credits/fx";

const METHOD_LABELS: Record<PaymentMethod, string> = {
  cashfree: "Cashfree",
  nowpayments: "NOWPayments",
  paypal: "PayPal",
  stripe: "Stripe",
};

const dateFmt = new Intl.DateTimeFormat("en", {
  dateStyle: "long",
  timeStyle: "short",
});

// A receipt states the row's real credit_ledger status rather than always
// "Paid" -- a printed document asserting payment for a failed or still-pending
// top-up is the one thing this page must never produce.
const STATUS_LABELS: Record<PurchaseStatus, string> = {
  completed: "Paid",
  pending: "Pending",
  partial: "Partially paid",
  refunded: "Refunded",
  failed: "Failed",
  expired: "Expired",
};

// The modal is dark on-screen (design system), but a printed receipt must be
// legible on paper.
//
// Printing does NOT print the <dialog>. The dialog lives deep inside the app's
// React tree, so a print rule that hides everything but it (`body > *:not(...)`)
// hides its own ancestors and takes the receipt down with them — that's why
// printing produced a blank page. On top of that, an open modal dialog sits in
// the browser's top layer, which print engines handle inconsistently.
//
// Instead, the same receipt data is rendered a second time into a plain,
// print-only sheet portalled directly to <body> (always a real body child, never
// in the top layer). Screen hides it; print hides everything else and shows it,
// already black-on-white so nothing depends on the app's dark theme variables.
const RECEIPT_CSS = `
/* margin:auto re-centres the modal -- Tailwind's preflight resets the native
   dialog's default centring margin, which otherwise pins it to the top-left. */
.receipt-dialog { margin: auto; }
.receipt-dialog[open] { animation: receipt-in 0.22s var(--ease); }
.receipt-dialog::backdrop { background: rgba(8,7,12,0.7); backdrop-filter: blur(4px); }
@keyframes receipt-in {
  from { opacity: 0; transform: translateY(8px) scale(0.985); }
  to { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) {
  .receipt-dialog[open] { animation: none; }
}

.receipt-print-sheet { display: none; }

@media print {
  /* Hide the whole app, including the branch the dialog is nested in. */
  body > *:not(.receipt-print-sheet) { display: none !important; }
  /* The dialog is in the top layer, so display:none on its ancestors is not
     guaranteed to remove it -- hide it explicitly too. */
  .receipt-dialog { display: none !important; }
  .receipt-dialog::backdrop { display: none !important; background: none !important; }

  .receipt-print-sheet {
    display: block !important;
    position: static !important;
    margin: 0 !important;
    padding: 0 !important;
    width: 100% !important;
    background: #fff !important;
    color: #000 !important;
    font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
  }
  @page { margin: 18mm; }
}`;

const printRowStyle: React.CSSProperties = {
  display: "flex",
  justifyContent: "space-between",
  gap: 24,
  padding: "7px 0",
  borderBottom: "1px solid #ddd",
  fontSize: 12,
  color: "#000",
};

// The print-only rendering of the receipt. Styled with literal colours rather
// than theme variables: this markup only ever appears on paper, where the app's
// dark palette would come out illegible (or be dropped entirely by browsers
// that disable background graphics when printing).
function PrintSheet({ purchase }: { purchase: Purchase }) {
  const rows: [string, string][] = [
    ["Receipt ID", purchase.id],
    ["Date", dateFmt.format(new Date(purchase.createdAt))],
    // The GST split only exists for an INR payment. A crypto top-up is stored
    // USD-denominated with no INR figure at all, so there is nothing to split
    // and inventing one would put a fabricated tax line on a receipt.
    ...(purchase.amountINR !== undefined
      ? ([
          [
            "Subtotal (excl. GST)",
            `INR ${gstBreakdown(purchase.amountINR).base.toFixed(2)}`,
          ],
          [
            "GST (18%)",
            `INR ${gstBreakdown(purchase.amountINR).gst.toFixed(2)}`,
          ],
          ["Amount paid", `INR ${purchase.amountINR.toFixed(2)}`],
        ] as [string, string][])
      : ([
          ["Amount paid", `USD ${(purchase.amountUSD ?? 0).toFixed(2)}`],
        ] as [string, string][])),
    ["Credits added", `USD ${purchase.creditsUSD.toFixed(2)}`],
    ["Method", METHOD_LABELS[purchase.method]],
    ["Status", STATUS_LABELS[purchase.status]],
  ];

  return (
    <div className="receipt-print-sheet" aria-hidden>
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          borderBottom: "2px solid #000",
          paddingBottom: 10,
          marginBottom: 16,
        }}
      >
        <span style={{ fontSize: 20, fontWeight: 700, color: "#000" }}>
          AgentMesh
        </span>
        <span
          style={{
            fontSize: 11,
            textTransform: "uppercase",
            letterSpacing: "0.12em",
            color: "#000",
          }}
        >
          Receipt
        </span>
      </div>

      <div>
        {rows.map(([label, value]) => (
          <div key={label} style={printRowStyle}>
            <span style={{ color: "#444" }}>{label}</span>
            <span
              style={{
                color: "#000",
                fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
              }}
            >
              {value}
            </span>
          </div>
        ))}
      </div>

      <p style={{ fontSize: 10, color: "#444", margin: "16px 0 0" }}>
        Mock receipt: not a valid tax invoice.
      </p>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div
      style={{
        display: "flex",
        justifyContent: "space-between",
        gap: 16,
        padding: "8px 0",
        borderBottom: "1px solid var(--border-soft)",
        fontSize: 13,
      }}
    >
      <span style={{ color: "var(--fg-muted)" }}>{label}</span>
      <span style={{ color: "var(--fg)", fontFamily: "var(--font-mono)" }}>
        {value}
      </span>
    </div>
  );
}

export function Receipt({
  purchase,
  onClose,
}: {
  purchase: Purchase;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const dlg = dialogRef.current;
    if (!dlg) return;
    if (!dlg.open) dlg.showModal();
    const onCancel = () => onClose();
    dlg.addEventListener("cancel", onCancel);
    return () => {
      dlg.removeEventListener("cancel", onCancel);
      if (dlg.open) dlg.close();
    };
  }, [onClose]);

  return (
    <>
      {/* Portalled to <body> so print can isolate it; see RECEIPT_CSS. The
          document guard is for the server render, where there is no portal
          target — it costs nothing at hydration since a portal contributes no
          markup to this position either way. */}
      {typeof document !== "undefined" &&
        createPortal(<PrintSheet purchase={purchase} />, document.body)}
      <style>{RECEIPT_CSS}</style>
      <dialog
        ref={dialogRef}
        className="receipt-dialog"
        aria-label="Receipt"
        onClick={(e) => {
          if (e.target === dialogRef.current) onClose();
        }}
        style={{
          padding: 0,
          border: "1px solid var(--border-strong)",
          borderRadius: "var(--r-4)",
          background: "var(--bg-elev-1)",
          color: "var(--fg)",
          width: "min(420px, calc(100vw - 48px))",
          maxWidth: "min(420px, calc(100vw - 48px))",
          boxShadow: "0 24px 64px rgba(0,0,0,0.5)",
        }}
      >
        <div style={{ padding: 24 }}>
          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              justifyContent: "space-between",
              marginBottom: 16,
            }}
          >
            <span style={{ fontSize: 16, fontWeight: 700, color: "var(--fg)" }}>
              AgentMesh
            </span>
            <span
              style={{
                fontSize: 12,
                textTransform: "uppercase",
                letterSpacing: "0.08em",
                color: "var(--fg-dim)",
              }}
            >
              Receipt
            </span>
          </div>

          <div>
            <Row label="Receipt ID" value={purchase.id} />
            <Row
              label="Date"
              value={dateFmt.format(new Date(purchase.createdAt))}
            />
            {purchase.amountINR !== undefined ? (
              <>
                <Row
                  label="Subtotal (excl. GST)"
                  value={`₹${gstBreakdown(purchase.amountINR).base.toFixed(2)}`}
                />
                <Row
                  label="GST (18%)"
                  value={`₹${gstBreakdown(purchase.amountINR).gst.toFixed(2)}`}
                />
                <Row
                  label="Amount paid"
                  value={`₹${purchase.amountINR.toFixed(2)}`}
                />
              </>
            ) : (
              <Row
                label="Amount paid"
                value={`$${(purchase.amountUSD ?? 0).toFixed(2)}`}
              />
            )}
            <Row
              label="Credits added"
              value={`$${purchase.creditsUSD.toFixed(2)}`}
            />
            <Row label="Method" value={METHOD_LABELS[purchase.method]} />
            <Row label="Status" value={STATUS_LABELS[purchase.status]} />
          </div>

          <p
            style={{ fontSize: 11, color: "var(--fg-dim)", margin: "14px 0 0" }}
          >
            Mock receipt: not a valid tax invoice.
          </p>

          <div
            style={{
              display: "flex",
              gap: 10,
              marginTop: 20,
              justifyContent: "flex-end",
            }}
          >
            <button
              type="button"
              onClick={() => window.print()}
              style={{
                height: 36,
                padding: "0 16px",
                borderRadius: "var(--r-2)",
                border: "1px solid var(--accent-line)",
                background: "var(--accent)",
                color: "var(--accent-fg)",
                fontSize: 13,
                fontWeight: 600,
                cursor: "pointer",
              }}
            >
              Print
            </button>
            <button
              type="button"
              onClick={onClose}
              style={{
                height: 36,
                padding: "0 16px",
                borderRadius: "var(--r-2)",
                border: "1px solid var(--border-strong)",
                background: "transparent",
                color: "var(--fg-muted)",
                fontSize: 13,
                fontWeight: 500,
                cursor: "pointer",
              }}
            >
              Close
            </button>
          </div>
        </div>
      </dialog>
    </>
  );
}
