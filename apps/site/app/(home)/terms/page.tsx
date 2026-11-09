import Link from "next/link"
import { ContactEmail, LegalPage, legalMetadata } from "@/components/legal-page"

const title = "Terms of service"
const summary = "The short rules for using Recall, a personal service that runs by invite only."

export const metadata = legalMetadata({ path: "/terms", title, description: summary })

export default function TermsPage() {
  return (
    <LegalPage title={title} summary={summary}>
      <p>
        Recall is a memory engine for AI agents at <code>recall.zakiego.com</code>. I&apos;m Zaki, and I run it myself as a
        personal, self-hosted service. By using Recall you agree to these terms. If you don&apos;t, please don&apos;t use it.
      </p>

      <h2 id="invite-only">Invite only</h2>
      <p>
        Accounts are by invitation. An invite is for you, so don&apos;t share your account, your password or your API keys. You
        are responsible for what happens with them, so revoke a key as soon as you think it has leaked.
      </p>

      <h2 id="as-is">Provided as is</h2>
      <p>
        Recall is a personal project, provided as is, without any warranty. It may be slow, go down, lose data or change without
        notice, and facts it extracts or answers it gives can be wrong. Keep your own copy of anything you can&apos;t afford to
        lose. As far as the law allows, I&apos;m not liable for any loss or damage that comes from using Recall.
      </p>

      <h2 id="acceptable-use">Acceptable use</h2>
      <p>Don&apos;t use Recall to:</p>
      <ul>
        <li>break the law, or store content you don&apos;t have the right to store;</li>
        <li>store other people&apos;s personal data without a good reason and their consent where it&apos;s needed;</li>
        <li>try to get into accounts, workspaces or data that aren&apos;t yours;</li>
        <li>attack, overload or probe the service, or get around its limits;</li>
        <li>resell access to it.</li>
      </ul>

      <h2 id="your-content">Your content</h2>
      <p>
        You own what you add. You let Recall store and process it only to provide the service to you and your workspace, as the{" "}
        <Link href="/privacy">privacy policy</Link> describes. Anything you add to a workspace can be read by its members.
      </p>

      <h2 id="google-data">Google data</h2>
      <p>
        If you connect Gmail, Recall handles that data as the <Link href="/privacy#google-user-data">privacy policy</Link>{" "}
        describes, including the Google API Services User Data Policy and its Limited Use requirements.
      </p>

      <h2 id="ending-access">Ending access</h2>
      <p>
        You can stop using Recall at any time and ask me to delete your account. I can remove your access or close Recall
        altogether, at any time and for any reason, and I&apos;ll try to give notice first so you can take your data with you.
      </p>

      <h2 id="changes">Changes to these terms</h2>
      <p>
        I may update these terms. When I do, I&apos;ll change this page and the date at the top. Using Recall after a change
        means you accept the new terms.
      </p>

      <h2 id="contact">Contact</h2>
      <p>
        Questions about these terms: <ContactEmail />.
      </p>
    </LegalPage>
  )
}
