import type { Metadata } from "next";
import Link from "next/link";
import styles from "@/components/settings/SettingsPage.module.css";

export const metadata: Metadata = {
  title: "Delete your account | AgentMesh",
  description: "Delete your AgentMesh account and associated data from the app or the web.",
};

export default function DeleteAccountPage() {
  return (
    <main className={styles.page}>
      <Link href="/" className={styles.back}>AgentMesh</Link>
      <h1>Delete your AgentMesh account</h1>
      <section className={styles.card} aria-labelledby="delete-online">
        <h2 id="delete-online">Delete online or in the app</h2>
        <p>
          You can delete your account here without installing the app.
          Sign in, open the profile icon, then choose Delete account.
          Type DELETE exactly and enter your current password if your account has one.
        </p>
        <p>
          <Link href="/signin?next=%2Fsettings">Sign in to delete your account</Link>
        </p>
        <p>
          Release any active machines in the Tendril console first.
          Deletion is permanent and any remaining credit balance will be lost.
        </p>
        <h2>What gets deleted</h2>
        <p>
          On successful confirmation, we immediately remove your account from the
          application database, together with your workflows, runs and logs,
          chat history, saved credentials, wallet keys, device registrations,
          credit and payment records, released machine history and matching waitlist entry.
          Your sessions stop working.
        </p>
        <p>
          Public blockchain transactions cannot be erased. Data already sent to
          connected services and copies in operational logs or backups require
          separate handling. Contact us for help with those copies or if you
          cannot sign in or release a machine.
        </p>
        <p>
          <a href="mailto:privacy@agent-mesh.app?subject=AgentMesh%20account%20deletion">
            Request deletion help: privacy@agent-mesh.app
          </a>
        </p>
        <p><Link href="/privacy">Privacy policy</Link></p>
      </section>
    </main>
  );
}
