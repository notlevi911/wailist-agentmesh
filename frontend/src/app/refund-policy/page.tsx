import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "Cancellation & Refund Policy | AgentMesh",
  description:
    "AgentMesh prepaid credit purchase policy: all sales are final and non-refundable once provisioned.",
};

const EFFECTIVE = "28 July 2026";
const COMPANY = "AgentMesh";
const CONTACT = "legal@agentmesh.ai";

export default function RefundPolicyPage() {
  return (
    <div
      style={{
        background: "var(--bg)",
        minHeight: "100dvh",
        color: "var(--fg)",
      }}
    >
      {/* Nav bar */}
      <div
        style={{
          borderBottom: "1px solid var(--border)",
          padding: "16px 32px",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <Link
          href="/"
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 14,
            fontWeight: 700,
            color: "var(--fg)",
            textDecoration: "none",
            letterSpacing: "-0.01em",
          }}
        >
          ← AgentMesh
        </Link>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--fg-dim)",
          }}
        >
          Effective {EFFECTIVE}
        </span>
      </div>

      <main
        style={{ maxWidth: 740, margin: "0 auto", padding: "64px 32px 96px" }}
      >
        {/* Header */}
        <div style={{ marginBottom: 56 }}>
          <div
            style={{
              display: "inline-block",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              letterSpacing: "0.1em",
              textTransform: "uppercase",
              color: "var(--accent)",
              background: "var(--accent-soft)",
              border: "1px solid var(--accent-line)",
              borderRadius: 999,
              padding: "3px 12px",
              marginBottom: 20,
            }}
          >
            Legal
          </div>
          <h1
            style={{
              margin: 0,
              fontSize: 40,
              fontWeight: 600,
              letterSpacing: "-0.025em",
              lineHeight: 1.15,
            }}
          >
            Cancellation &amp; Refund Policy
          </h1>
          <p
            style={{
              margin: "12px 0 0",
              color: "var(--fg-muted)",
              fontSize: 15,
              lineHeight: 1.6,
            }}
          >
            This policy applies to all Credit purchases made on the {COMPANY}{" "}
            Platform. Please read it carefully before completing any purchase.
          </p>
        </div>

        {/* TL;DR banner */}
        <div
          style={{
            padding: "20px 24px",
            background: "var(--bg-elev-1)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-3)",
            marginBottom: 48,
            display: "flex",
            gap: 16,
            alignItems: "flex-start",
          }}
        >
          <span style={{ fontSize: 20, flexShrink: 0, marginTop: 1 }}>⚠</span>
          <div>
            <div style={{ fontWeight: 600, fontSize: 14, marginBottom: 4 }}>
              Important: All credit purchases are final.
            </div>
            <div
              style={{
                fontSize: 13.5,
                lineHeight: 1.65,
                color: "var(--fg-muted)",
              }}
            >
              Once Credits are provisioned to your account, the transaction is
              complete and non-reversible. We do not offer refunds, exchanges,
              or cash conversions for any prepaid Credits.
            </div>
          </div>
        </div>

        <PolicyDoc />

        {/* Footer nav */}
        <div
          style={{
            marginTop: 64,
            paddingTop: 32,
            borderTop: "1px solid var(--border)",
            display: "flex",
            gap: 24,
            flexWrap: "wrap",
          }}
        >
          <Link href="/terms" style={footerLink}>
            Terms &amp; Conditions →
          </Link>
          <a href={`mailto:${CONTACT}`} style={footerLink}>
            {CONTACT}
          </a>
        </div>
      </main>
    </div>
  );
}

function PolicyDoc() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 0 }}>
      <Section n="1" title="What We Sell">
        <P>
          {COMPANY} sells prepaid platform Credits (&ldquo;Credits&rdquo;) that
          are deducted in real time as your agent workflows consume
          computational resources. Credits are a finite, platform-internal
          resource, not a subscription and not a financial product.
        </P>
      </Section>

      <Section n="2" title="All Sales Are Final" highlight>
        <P>
          <strong>
            All Credit purchases are strictly final and non-refundable.
          </strong>{" "}
          Once a payment is confirmed and Credits are provisioned to your{" "}
          {COMPANY} account, the sale is complete. We do not issue refunds,
          partial refunds, or credit adjustments for:
        </P>
        <ul
          style={{
            margin: "10px 0 0",
            paddingLeft: 20,
            color: "var(--fg-muted)",
            fontSize: 14.5,
            lineHeight: 1.8,
          }}
        >
          <li>Credits purchased but not yet used</li>
          <li>Credits consumed by failed, stopped, or errored workflow runs</li>
          <li>Credits remaining at the time of voluntary account closure</li>
          <li>Dissatisfaction with AI model output quality</li>
          <li>Accidental purchases or incorrect top-up amounts</li>
          <li>Pricing changes after a purchase is made</li>
        </ul>
        <Notice>
          Before purchasing Credits, please verify the amount you intend to buy.
          We recommend starting with a small top-up to evaluate the Platform
          before committing to a larger balance.
        </Notice>
      </Section>

      <Section n="3" title="No Cancellation Window">
        <P>
          Because Credits are a digital, instantly provisioned resource, there
          is no cooling-off or cancellation window after purchase. The digital
          delivery is complete at the moment of payment confirmation, which
          satisfies the conditions for exclusion from statutory cancellation
          rights under applicable consumer protection rules for digital content.
        </P>
      </Section>

      <Section n="4" title="No Cash Conversion">
        <P>
          Credits hold <strong>zero monetary value</strong> and cannot be
          converted, withdrawn, or exchanged for cash, fiat currency, or any
          other monetary form under any circumstances. This applies to unused
          Credits, remaining Credits upon account closure, and Credits obtained
          through any promotional or bonus mechanism.
        </P>
      </Section>

      <Section n="5" title="Disputed or Failed Payments">
        <P>
          If your payment fails mid-transaction, no Credits are provisioned and
          no amount is charged. If your bank or payment processor debits your
          account despite a transaction failure, please contact us at{" "}
          <a href={`mailto:${CONTACT}`} style={inlineLink}>
            {CONTACT}
          </a>{" "}
          with your transaction reference and we will investigate promptly.
        </P>
        <P>
          Initiating a chargeback or payment dispute for a legitimately
          completed Credit purchase, where Credits were provisioned as
          described, is a breach of these Terms and may result in immediate
          suspension of your account.
        </P>
      </Section>

      <Section n="6" title="Exceptions">
        <P>
          We may, at our sole discretion, issue a Credit adjustment (not a cash
          refund) in the following circumstances:
        </P>
        <ul
          style={{
            margin: "10px 0 0",
            paddingLeft: 20,
            color: "var(--fg-muted)",
            fontSize: 14.5,
            lineHeight: 1.8,
          }}
        >
          <li>
            A confirmed platform bug caused Credits to be debited for runs that
            did not execute
          </li>
          <li>
            Duplicate charges caused by a verified payment processing error on
            our end
          </li>
        </ul>
        <P style={{ marginTop: 12 }}>
          In such cases, the remedy will be a restoration of Credits, not a cash
          payment, unless otherwise required by applicable law.
        </P>
      </Section>

      <Section n="7" title="Promotional Credits">
        <P>
          Credits awarded through promotions, trials, referrals, or bonuses
          carry the same restrictions as purchased Credits. They are
          non-refundable, have no cash value, and expire on the date specified
          in the promotional terms (or when your account is closed, whichever is
          earlier).
        </P>
      </Section>

      <Section n="8" title="Statutory Rights">
        <P>
          Nothing in this policy excludes or limits any right you may have under
          applicable consumer protection legislation that cannot be waived by
          contract. If you believe you have a statutory right to a refund,
          please contact us at{" "}
          <a href={`mailto:${CONTACT}`} style={inlineLink}>
            {CONTACT}
          </a>{" "}
          and we will assess your request in accordance with applicable law.
        </P>
      </Section>

      <Section n="9" title="How to Contact Us">
        <P>
          For billing questions, payment discrepancies, or concerns about this
          policy, email us at{" "}
          <a href={`mailto:${CONTACT}`} style={inlineLink}>
            {CONTACT}
          </a>
          . Please include your registered email address and, if applicable, the
          transaction reference from your payment provider. We aim to respond
          within 2 business days.
        </P>
      </Section>
    </div>
  );
}

// ── Sub-components ────────────────────────────────────────────────────────────

function Section({
  n,
  title,
  children,
  highlight,
}: {
  n: string;
  title: string;
  children: React.ReactNode;
  highlight?: boolean;
}) {
  return (
    <div
      style={{
        padding: "32px 0",
        borderBottom: "1px solid var(--border-soft)",
        ...(highlight
          ? {
              margin: "8px -24px",
              padding: "32px 24px",
              background: "var(--bg-elev-1)",
              borderRadius: "var(--r-3)",
              border: "1px solid var(--border)",
              borderBottom: "1px solid var(--border)",
            }
          : {}),
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          gap: 12,
          marginBottom: 16,
        }}
      >
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--accent)",
            background: "var(--accent-soft)",
            border: "1px solid var(--accent-line)",
            borderRadius: 999,
            padding: "2px 8px",
            flexShrink: 0,
          }}
        >
          {n}
        </span>
        <h2
          style={{
            margin: 0,
            fontSize: 20,
            fontWeight: 600,
            letterSpacing: "-0.015em",
          }}
        >
          {title}
        </h2>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {children}
      </div>
    </div>
  );
}

function P({
  children,
  style,
}: {
  children: React.ReactNode;
  style?: React.CSSProperties;
}) {
  return (
    <p
      style={{
        margin: 0,
        fontSize: 14.5,
        lineHeight: 1.75,
        color: "var(--fg-muted)",
        ...style,
      }}
    >
      {children}
    </p>
  );
}

function Notice({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        marginTop: 16,
        padding: "12px 16px",
        background: "rgba(255,181,71,0.08)",
        border: "1px solid rgba(255,181,71,0.25)",
        borderRadius: "var(--r-2)",
        fontSize: 13,
        lineHeight: 1.6,
        color: "var(--fg)",
      }}
    >
      {children}
    </div>
  );
}

const footerLink: React.CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 12,
  color: "var(--fg-muted)",
  textDecoration: "none",
};

const inlineLink: React.CSSProperties = {
  color: "var(--accent)",
  textDecoration: "none",
};
