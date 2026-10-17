import type { Metadata } from "next";
import Link from "next/link";

// The privacy policy.
//
// Written because Google Play requires a reachable privacy-policy URL the
// moment an app declares ACCESS_BACKGROUND_LOCATION, which the Android app
// does (#134). It is also the document Play's Data Safety form is checked
// against, so the location section below is deliberately specific: it states
// what the server actually keeps -- a derived enter/leave boolean and a
// timestamp, never coordinates -- because that is what migration 000029 stores
// and what the in-app disclosure already promises.
//
// The presentational helpers here are local rather than shared with /terms.
// Extracting them would mean editing a live legal page to no functional end;
// if a third legal page ever appears, that is the moment to factor them out.

export const metadata: Metadata = {
  title: "Privacy Policy | AgentMesh",
  description:
    "How AgentMesh handles your data, including location used for workflow triggers.",
};

const EFFECTIVE = "6 October 2026";
const COMPANY = "AgentMesh";
const CONTACT = "privacy@agent-mesh.app";

export default function PrivacyPage() {
  return (
    <div
      style={{
        background: "var(--bg)",
        minHeight: "100dvh",
        color: "var(--fg)",
      }}
    >
      <div
        className="am-legal-bar"
        style={{
          borderBottom: "1px solid var(--border)",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <Link
          href="/"
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "var(--t-4)",
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
            fontSize: "var(--t-1)",
            color: "var(--fg-dim)",
          }}
        >
          Effective {EFFECTIVE}
        </span>
      </div>

      <main className="am-legal-main">
        <div style={{ marginBottom: 56 }}>
          <div style={badge}>Legal</div>
          <h1
            style={{
              margin: 0,
              fontSize: "var(--t-8)",
              fontWeight: 600,
              letterSpacing: "-0.025em",
              lineHeight: 1.15,
            }}
          >
            Privacy Policy
          </h1>
          <p
            style={{
              margin: "12px 0 0",
              color: "var(--fg-muted)",
              fontSize: "var(--t-4)",
              lineHeight: 1.6,
            }}
          >
            What {COMPANY} collects, what it does not, and what you can do about
            it.
          </p>
        </div>

        <div style={{ display: "flex", flexDirection: "column" }}>
          <Section n="1" title="What this covers">
            <P>
              This policy covers the {COMPANY} web application at agent-mesh.app
              and the {COMPANY} Android app. Where the two differ (and on
              location, they differ considerably) the difference is stated.
            </P>
            <P>
              The Android app runs and monitors workflows; it does not build
              them. Creating or editing a workflow, and connecting or
              disconnecting the services it uses, is done in the {COMPANY}{" "}
              desktop app at agent-mesh.app. So the workflow content and
              connected-account data described below are entered there, not on
              your phone.
            </P>
          </Section>

          <Section n="2" title="Information you give us">
            <P>
              <strong>Account details.</strong> Your email address, the name you
              give us, and, if you give one, the name of your organisation. If
              you sign up with a password, we keep only a hash of it, never the
              password itself.
            </P>
            <P>
              <strong>Signing in with Google or GitHub.</strong> We ask the
              provider only for your verified email address, and use it to find
              or create your account. We do not receive or store your Google or
              GitHub password.
            </P>
            <P>
              <strong>Workflow content.</strong> The workflows you build, the
              configuration you enter into them, and the record of the runs they
              produce. Credentials you enter for third-party connectors are
              encrypted before storage.
            </P>
            <P>
              <strong>Connected accounts.</strong> If you connect a service such
              as Google (Gmail sending, Sheets, Calendar), Slack, Notion or Jira
              so a workflow can use it, we store the access tokens that service
              issues, encrypted. They are used only to perform the actions your
              workflows are configured to take, such as sending an email or
              updating a spreadsheet, and only when those workflows run. We do
              not read your Gmail messages or access your Google Drive. You can
              disconnect at any time from the desktop app, and revoke access
              from the service&rsquo;s own account settings. {COMPANY}&rsquo;s
              use of information received from Google APIs adheres to the{" "}
              <a
                href="https://developers.google.com/terms/api-services-user-data-policy"
                style={inlineLink}
              >
                Google API Services User Data Policy
              </a>
              , including the Limited Use requirements.
            </P>
            <P>
              <strong>Billing information.</strong> Records of credits purchased
              and consumed, and of on-chain payments made by your workflows.
            </P>
          </Section>

          <Section n="3" title="Google user data">
            <P>
              This section applies if you connect a Google account to a
              workflow. It covers data {COMPANY} receives through Google APIs.
            </P>
            <P>
              <strong>What we access.</strong> Only what the Google steps in
              your workflows need, using three permissions: sending email from
              your Gmail account (we cannot read, list or delete your mail),
              reading and adding rows in Google Sheets you specify, and listing
              and creating events in your Google Calendar. We also receive the
              email address of the Google account you connect, to show you which
              account a workflow uses. We do not access Google Drive.
            </P>
            <P>
              <strong>How we use it.</strong> Only to carry out the step your
              workflow is configured to perform, when that workflow runs: for
              example sending the email it composes, appending its result to a
              sheet, or creating the event it describes.
            </P>
            <P>
              <strong>Who we share it with.</strong> We do not sell Google user
              data or share it with third parties, except as needed to run the
              workflow you built: if your workflow passes the result of a Google
              step to an AI model or another connected service, that result is
              sent there because you configured it to be. We do not use Google
              user data for advertising, credit or lending decisions, or to
              train AI or machine-learning models, and no person at {COMPANY}
              reads it unless you ask us to for support, or the law requires it.
            </P>
            <P>
              <strong>How we protect it.</strong> Google access and refresh
              tokens are encrypted at rest and only used server-side to make the
              API calls your workflows request. All traffic is encrypted in
              transit.
            </P>
            <P>
              <strong>Retention and deletion.</strong> Tokens are kept until you
              disconnect the Google account, at which point we delete them; you
              can also revoke access at any time from your Google
              Account&rsquo;s security settings. Results of Google steps, such
              as rows read from a sheet, can appear in that workflow&rsquo;s run
              history, which is deleted when you delete the workflow or your
              account.
            </P>
            <P>
              {COMPANY}&rsquo;s use and transfer of information received from
              Google APIs adheres to the{" "}
              <a
                href="https://developers.google.com/terms/api-services-user-data-policy"
                style={inlineLink}
              >
                Google API Services User Data Policy
              </a>
              , including the Limited Use requirements.
            </P>
          </Section>

          <Section n="4" title="Location, in the Android app">
            <P>
              This section exists because it is the part people most want a
              straight answer about.
            </P>
            <P>
              The Android app can start a workflow when you cross the edge of a
              place you have chosen. To do that, Android must be allowed to
              check your location while the app is closed. This is entirely
              optional: everything else in the app works without it, and the
              feature stays off until you turn it on.
            </P>
            <Notice>
              <strong>We do not keep a record of where you have been.</strong>{" "}
              When your device reports a position, it is used to answer one
              question, whether you are inside the zone you chose or outside it,
              and then discarded. What our servers retain is that answer and the
              time it was given. There is no history of coordinates for us to
              search, hand over, or lose.
            </Notice>
            <P>
              <strong>On your device.</strong> A crossing that happens with no
              signal is held on the phone until it can be sent, so it is not
              lost. Those pending readings do include your position. They never
              leave the device except to report that one crossing, and each is
              deleted as soon as it is sent, or within a day if it never can be.
            </P>
            <P>
              <strong>Turning it off.</strong> Remove the zone in the app, or
              revoke location permission in Android settings. Removing the zone
              also clears the state described above.
            </P>
          </Section>

          <Section n="5" title="Notifications">
            <P>
              If you allow notifications, your device is issued a registration
              token by Google&rsquo;s Firebase Cloud Messaging, and we store
              that token so we can tell your device when one of your workflows
              finishes. It identifies the app installation, not you. Signing out
              removes it, and a token that stops working is deleted.
            </P>
          </Section>

          <Section n="6" title="What we do not do">
            <P>
              We do not sell your personal information. We do not share it with
              third parties for their own advertising or marketing. We do not
              use your workflow content to train machine-learning models.
            </P>
          </Section>

          <Section n="7" title="Service providers">
            <P>
              Running the product means some data passes through others: hosting
              and database providers, Google&rsquo;s Firebase Cloud Messaging
              for notifications, payment and blockchain infrastructure for
              billing, and the AI model providers your workflows are configured
              to call. A workflow that calls an external model or tool sends
              that provider whatever the workflow gives it. You choose those
              connections, and their own privacy policies apply to them.
            </P>
          </Section>

          <Section n="8" title="Retention and deletion">
            <P>
              Account and workflow data is kept while your account is open.
              Deleting a workflow deletes its configuration and its run history.
              To delete your account and associated application data, use the
              profile menu&apos;s Delete account control, or visit our{" "}
              <Link href="/delete-account" style={inlineLink}>
                account deletion page
              </Link>
              . It explains what is removed, prerequisites and the web deletion
              path. For help with deletion, including copies held by connected
              services, operational logs or backups, write to{" "}
              <a href={`mailto:${CONTACT}`} style={inlineLink}>
                {CONTACT}
              </a>
              .
            </P>
          </Section>

          <Section n="9" title="Security">
            <P>
              Traffic is encrypted in transit. Connector credentials are
              encrypted at rest, and on Android the session token is held in
              storage encrypted with a key kept in the device&rsquo;s hardware
              keystore. No system is perfect, and we do not claim otherwise.
            </P>
          </Section>

          <Section n="10" title="Children">
            <P>
              {COMPANY} is not intended for children under 13, and we do not
              knowingly collect their information.
            </P>
          </Section>

          <Section n="11" title="Changes">
            <P>
              If this policy changes materially, we will update the effective
              date above and notify account holders. Continuing to use {COMPANY}{" "}
              after a change means you accept it.
            </P>
          </Section>

          <Section n="12" title="Contact">
            <P>
              Questions, requests, or complaints:{" "}
              <a href={`mailto:${CONTACT}`} style={inlineLink}>
                {CONTACT}
              </a>
              .
            </P>
          </Section>
        </div>

        <div
          style={{
            marginTop: 64,
            paddingTop: 32,
            borderTop: "1px solid var(--border)",
            display: "flex",
            gap: "var(--s-6)",
            fontFamily: "var(--font-mono)",
            fontSize: "var(--t-2)",
          }}
        >
          <Link href="/terms" style={inlineLink}>
            Terms &amp; Conditions
          </Link>
          <Link href="/refund-policy" style={inlineLink}>
            Cancellation &amp; Refund Policy
          </Link>
        </div>
      </main>
    </div>
  );
}

const badge: React.CSSProperties = {
  display: "inline-block",
  fontFamily: "var(--font-mono)",
  fontSize: "var(--t-1)",
  letterSpacing: "0.1em",
  textTransform: "uppercase",
  color: "var(--accent)",
  background: "var(--accent-soft)",
  border: "1px solid var(--accent-line)",
  borderRadius: "var(--r-full)",
  padding: "3px 12px",
  marginBottom: 20,
};

const inlineLink: React.CSSProperties = {
  color: "var(--accent)",
  textDecoration: "underline",
};

function Section({
  n,
  title,
  children,
}: {
  n: string;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section style={{ marginBottom: 40 }}>
      <h2
        style={{
          margin: "0 0 12px",
          fontSize: "var(--t-5)",
          fontWeight: 600,
          letterSpacing: "-0.01em",
          display: "flex",
          gap: "var(--s-4)",
          alignItems: "baseline",
        }}
      >
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "var(--t-2)",
            color: "var(--fg-dim)",
          }}
        >
          {n}
        </span>
        {title}
      </h2>
      {children}
    </section>
  );
}

function P({ children }: { children: React.ReactNode }) {
  return (
    <p
      style={{
        margin: "0 0 12px",
        color: "var(--fg-muted)",
        fontSize: "var(--t-4)",
        lineHeight: 1.7,
        maxWidth: "68ch",
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
        border: "1px solid var(--accent-line)",
        background: "var(--accent-soft)",
        borderRadius: "var(--r-2)",
        padding: "14px 16px",
        margin: "0 0 12px",
        color: "var(--fg)",
        fontSize: "var(--t-4)",
        lineHeight: 1.7,
        maxWidth: "68ch",
      }}
    >
      {children}
    </div>
  );
}
