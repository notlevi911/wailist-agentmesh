import type { Metadata } from "next";
import Link from "next/link";

export const metadata: Metadata = {
  title: "Terms & Conditions | AgentMesh",
  description:
    "AgentMesh platform terms of service, including credits policy and acceptable use.",
};

const EFFECTIVE = "28 July 2026";
const COMPANY = "AgentMesh";
const CONTACT = "legal@agentmesh.ai";

export default function TermsPage() {
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
            Terms &amp; Conditions
          </h1>
          <p
            style={{
              margin: "12px 0 0",
              color: "var(--fg-muted)",
              fontSize: 15,
              lineHeight: 1.6,
            }}
          >
            Please read these terms carefully before using {COMPANY}. By
            accessing or using the platform, you agree to be bound by these
            terms.
          </p>
        </div>

        <LegalDoc />

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
          <Link href="/refund-policy" style={linkStyle}>
            Cancellation &amp; Refund Policy →
          </Link>
          <a href={`mailto:${CONTACT}`} style={linkStyle}>
            {CONTACT}
          </a>
        </div>
      </main>
    </div>
  );
}

function LegalDoc() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 0 }}>
      <Section n="1" title="Acceptance of Terms">
        <P>
          By creating an account or using {COMPANY} (the
          &ldquo;Platform&rdquo;), you agree to these Terms &amp; Conditions and
          our{" "}
          <Link href="/refund-policy" style={inlineLink}>
            Cancellation &amp; Refund Policy
          </Link>
          , which are incorporated by reference. If you do not agree, you must
          not use the Platform.
        </P>
      </Section>

      <Section n="2" title="Description of Service">
        <P>
          {COMPANY} provides a visual canvas for designing, deploying, and
          monitoring autonomous AI agent workflows. The Platform enables users
          to connect AI agents, tools, and data sources into executable
          pipelines.
        </P>
      </Section>

      <Section n="3" title="Account Registration">
        <P>
          You must provide accurate, complete, and current information when
          registering. You are responsible for maintaining the confidentiality
          of your credentials and for all activity that occurs under your
          account. You must be at least 18 years old to use the Platform.
        </P>
      </Section>

      {/* -- CREDITS: the section Cashfree reviewers care most about -- */}
      <Section n="4" title="Platform Credits" highlight>
        <P>
          The following terms govern the purchase and use of {COMPANY} Credits
          (&ldquo;Credits&rdquo;). By purchasing Credits you explicitly
          acknowledge and accept each condition below.
        </P>

        <SubSection title="4.1 Nature of Credits">
          <P>
            Credits are a prepaid, platform-internal unit of account used solely
            to pay for computational resources consumed by your agent workflows
            on the {COMPANY} Platform. Credits are <strong>not</strong>{" "}
            currency, legal tender, or a financial instrument of any kind.
          </P>
        </SubSection>

        <SubSection title="4.2 Exclusive Platform Use">
          <P>
            Credits may <strong>only</strong> be redeemed against metered
            workflow execution charges incurred on the {COMPANY} Platform.
            Credits have no use or value outside the Platform and cannot be
            applied to any external product, service, or purpose.
          </P>
        </SubSection>

        <SubSection title="4.3 No Monetary or Cash Value">
          <P>
            Credits carry <strong>zero monetary value</strong>. They cannot be
            exchanged, converted, or redeemed for cash, fiat currency,
            cryptocurrency, or any other monetary equivalent, either by{" "}
            {COMPANY} or any third party, under any circumstances.
          </P>
        </SubSection>

        <SubSection title="4.4 Non-Transferability">
          <P>
            Credits are strictly <strong>non-transferable</strong>. You may not
            sell, gift, assign, trade, barter, or otherwise transfer Credits to
            any other user or third party, whether for consideration or
            gratuitously. Any purported transfer is void.
          </P>
        </SubSection>

        <SubSection title="4.5 No Withdrawal">
          <P>
            Credits <strong>cannot be withdrawn</strong> as cash or converted
            into any monetary form. Unused Credits do not entitle you to a cash
            payment or refund from {COMPANY} except as required by applicable
            law.
          </P>
        </SubSection>

        <SubSection title="4.6 Expiry">
          <P>
            Credits do not expire while your account remains in good standing.{" "}
            {COMPANY} reserves the right to introduce Credit expiry with not
            less than 90 days&apos; advance written notice to affected users.
          </P>
        </SubSection>

        <SubSection title="4.7 Account Closure">
          <P>
            Upon voluntary closure of your account, any remaining Credit balance
            is forfeited without compensation. {COMPANY} encourages you to
            deplete your Credit balance before closing your account.
          </P>
        </SubSection>

        <SubSection title="4.8 Final Sales: No Refunds">
          <P>
            All Credit purchases are <strong>final and non-refundable</strong>{" "}
            once the Credits are provisioned to your account. Please see our{" "}
            <Link href="/refund-policy" style={inlineLink}>
              Cancellation &amp; Refund Policy
            </Link>{" "}
            for full details.
          </P>
        </SubSection>

        <Notice>
          Summary: Credits are a prepaid platform resource, like fuel for your
          agent pipelines. They are not money, cannot become money, and cannot
          leave the Platform.
        </Notice>
      </Section>

      <Section n="5" title="Acceptable Use">
        <P>
          You agree not to use the Platform to: (a) violate any applicable law
          or regulation; (b) infringe the intellectual property rights of any
          third party; (c) transmit malicious code or interfere with the
          Platform&apos;s infrastructure; (d) reverse-engineer or attempt to
          extract the Platform&apos;s source code; (e) use the Platform to train
          competing AI models without express written permission.
        </P>
      </Section>

      <Section n="6" title="Intellectual Property">
        <P>
          {COMPANY} and its licensors own all intellectual property rights in
          the Platform. Nothing in these Terms transfers any such rights to you.
          You retain ownership of the content you upload or generate through the
          Platform, subject to a licence to {COMPANY} to operate the service.
        </P>
      </Section>

      <Section n="7" title="Pricing and Billing">
        <P>
          Workflow execution is billed against your Credit balance in real time.
          Pricing for individual tool calls and model usage is set by {COMPANY}{" "}
          and may change at any time without prior notice. {COMPANY} does not
          extend credit; you must maintain a positive Credit balance to run
          workflows.
        </P>
      </Section>

      <Section n="8" title="Disclaimer of Warranties">
        <P>
          The Platform is provided &ldquo;as is&rdquo; and &ldquo;as
          available&rdquo; without warranty of any kind, express or implied.{" "}
          {COMPANY} does not warrant that the Platform will be uninterrupted,
          error-free, or free of harmful components. AI-generated outputs are
          probabilistic and may be inaccurate; you are solely responsible for
          validating any output before relying on it.
        </P>
      </Section>

      <Section n="9" title="Limitation of Liability">
        <P>
          To the fullest extent permitted by applicable law, {COMPANY}&apos;s
          aggregate liability to you for any claim arising out of or relating to
          the Platform or these Terms shall not exceed the amount of Credits you
          purchased in the 12 months preceding the claim. {COMPANY} is not
          liable for indirect, incidental, special, consequential, or punitive
          damages.
        </P>
      </Section>

      <Section n="10" title="Termination">
        <P>
          Either party may terminate the relationship at any time. {COMPANY} may
          suspend or terminate your access for breach of these Terms,
          non-payment, or if required by law. Upon termination, sections 4, 6,
          8, 9, and 11 survive.
        </P>
      </Section>

      <Section n="11" title="Governing Law and Disputes">
        <P>
          These Terms are governed by the laws of India. Any dispute arising
          under or in connection with these Terms shall be subject to the
          exclusive jurisdiction of the courts located in Bengaluru, Karnataka,
          India.
        </P>
      </Section>

      <Section n="12" title="Changes to Terms">
        <P>
          {COMPANY} may update these Terms at any time. Material changes will be
          notified by email or in-app notice at least 14 days before they take
          effect. Continued use of the Platform after the effective date
          constitutes acceptance of the revised Terms.
        </P>
      </Section>

      <Section n="13" title="Contact">
        <P>
          Questions about these Terms should be directed to{" "}
          <a href={`mailto:${CONTACT}`} style={inlineLink}>
            {CONTACT}
          </a>
          .
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
      <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
        {children}
      </div>
    </div>
  );
}

function SubSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <h3
        style={{
          margin: "0 0 8px",
          fontSize: 13,
          fontWeight: 600,
          color: "var(--fg)",
          letterSpacing: "-0.01em",
        }}
      >
        {title}
      </h3>
      {children}
    </div>
  );
}

function P({ children }: { children: React.ReactNode }) {
  return (
    <p
      style={{
        margin: 0,
        fontSize: 14.5,
        lineHeight: 1.75,
        color: "var(--fg-muted)",
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
        marginTop: 8,
        padding: "12px 16px",
        background: "var(--accent-soft)",
        border: "1px solid var(--accent-line)",
        borderRadius: "var(--r-2)",
        fontSize: 13,
        lineHeight: 1.6,
        color: "var(--fg)",
        fontStyle: "italic",
      }}
    >
      {children}
    </div>
  );
}

const linkStyle: React.CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 12,
  color: "var(--fg-muted)",
  textDecoration: "none",
};

const inlineLink: React.CSSProperties = {
  color: "var(--accent)",
  textDecoration: "none",
};
