import Link from "next/link"
import { ContactEmail, LegalPage, legalMetadata } from "@/components/legal-page"

const title = "Privacy policy"
const summary =
  "What Recall collects, where it's kept, who can see it, what happens with Gmail data, and how to get any of it deleted."

export const metadata = legalMetadata({ path: "/privacy", title, description: summary })

const LIMITED_USE_URL = "https://developers.google.com/terms/api-services-user-data-policy"
const GOOGLE_PERMISSIONS_URL = "https://myaccount.google.com/permissions"

export default function PrivacyPage() {
  return (
    <LegalPage title={title} summary={summary}>
      <p>
        Recall is a memory engine for AI agents at <code>recall.zakiego.com</code>. I&apos;m Zaki, and I run it myself as a
        personal, self-hosted service for me and the people I invite. In this policy, &quot;I&quot; and &quot;me&quot; mean Zaki, and
        &quot;you&quot; means anyone with a Recall account or anyone visiting this site.
      </p>

      <h2 id="in-short">In short</h2>
      <ul>
        <li>
          Recall keeps your account, the content you add, the session details that keep you signed in, and, if you connect
          Gmail, your Google address and encrypted tokens.
        </li>
        <li>It lives on a server I run. Nightly backups go to Cloudflare R2 and are deleted after 30 days.</li>
        <li>Text you add is sent to an AI model to pull out facts. Search runs on the server.</li>
        <li>
          Gmail access is read-only, and Recall reads a whole message only when you import it, yourself or with an API key you
          created. See{" "}
          <a href="#google-user-data">Google user data</a>.
        </li>
        <li>Nothing is sold, nothing is used for ads, and there are no analytics or tracking cookies.</li>
        <li>
          Write to <ContactEmail /> and I&apos;ll delete your account and everything in it.
        </li>
      </ul>

      <h2 id="what-recall-collects">What Recall collects</h2>
      <ul>
        <li>
          <strong>Your account.</strong> Your username and display name, and how you sign in: a password, stored only as a salted
          hash, or a passkey, of which only the public key is stored. Recall also keeps which workspaces you belong to and your
          role in each. It doesn&apos;t ask for your email address.
        </li>
        <li>
          <strong>Content you add.</strong> Text, chats, links and the pages fetched from them, files you upload and Gmail messages
          you import. Recall also stores what it makes from them: facts and their version history, summaries, answers and the
          search index.
        </li>
        <li>
          <strong>API keys.</strong> Stored only as a SHA-256 hash. The key itself is shown to you once, when it&apos;s created.
        </li>
        <li>
          <strong>Sessions.</strong> A cookie keeps you signed in. Each session record stores the IP address and browser it was
          started from, so you can be signed out and abuse can be traced. Sessions end after 7 days without use.
        </li>
        <li>
          <strong>IP addresses for rate limiting.</strong> Sign-in and invite requests are counted per IP address, in memory only,
          to slow down password guessing. The counts are never written to disk.
        </li>
      </ul>
      <p>
        Recall doesn&apos;t use analytics, advertising trackers or third-party cookies, on this site or in the dashboard.
      </p>

      <h2 id="how-it-is-used">How it&apos;s used</h2>
      <p>
        Only to run Recall for you: sign you in, store what you add, turn it into facts, search it, answer questions about it,
        and keep the service working and secure. I don&apos;t sell personal data, use it for advertising, or build profiles of
        you for anyone else.
      </p>

      <h2 id="where-it-is-stored">Where it&apos;s stored</h2>
      <ul>
        <li>
          Recall keeps everything in one SQLite database, plus a folder of uploaded files, on a self-hosted server I own and
          run. There is no cloud database.
        </li>
        <li>
          The LLM gateway described under <a href="#ai-processing">AI processing</a> runs on the same server and keeps a log
          of each request it handles, including the text sent to the model, for 30 days.
        </li>
        <li>
          Traffic reaches it through Cloudflare&apos;s network over HTTPS. Cloudflare also hosts this public site, so it sees
          IP addresses and request details as part of carrying the traffic.
        </li>
        <li>
          Every night a snapshot of the database is compressed and uploaded to a private Cloudflare R2 bucket, where it&apos;s
          encrypted at rest. Each backup is deleted automatically after 30 days.
        </li>
      </ul>

      <h2 id="ai-processing">AI processing</h2>
      <p>
        To pull facts out of what you add, write summaries and answer questions, Recall sends the text to an LLM gateway I run,
        which passes it to Anthropic&apos;s Claude models and returns the result. Recall&apos;s Anthropic account has model
        training turned off, so this text isn&apos;t used to train Anthropic&apos;s models. Before content you add is saved as
        text or sent for fact extraction, Recall masks things that look like API keys, access tokens and card numbers. Search
        uses a small model that runs on the server itself, so nothing leaves it.
      </p>

      <h2 id="google-user-data">Google user data</h2>
      <p>This section only applies if you connect a Gmail account on the Connectors page of the dashboard.</p>

      <h3 id="google-access">What Recall can access</h3>
      <p>
        Recall asks Google for one permission: <code>gmail.readonly</code>. It can read your mail, and it can&apos;t send,
        delete, move or label anything. Nothing syncs in the background and nothing runs on a schedule.
      </p>
      <ul>
        <li>
          When you search on the Connectors page, Recall asks Gmail for the matching messages&apos; sender, subject, date and short
          preview, so it can show you the list. These search results aren&apos;t stored.
        </li>
        <li>
          Recall reads a whole message, and its PDF attachments, only when you pick that message and import it, retry an
          import you started, or import it by its ID with an API key you created. If Gmail doesn&apos;t answer, Recall tries
          that read again a few times over the next few minutes.
        </li>
        <li>
          Recall stores the address of the Google account you connected, so imports keep using the same mailbox. On the
          Connectors page, owners and admins of your workspace see that you connected Gmail and when, not your address or your
          mailbox. But each imported message keeps that address in its details, and often in its To line, so anyone in the
          workspace who can read the message, including their agents, can see it.
        </li>
      </ul>

      <h3 id="google-use">How it&apos;s used</h3>
      <p>
        An imported message becomes a document in the container you chose: its subject, sender, recipients, date and text. Each
        PDF attachment, up to 20 MB, becomes a document of its own. Recall then pulls facts out of them like any other content,
        through the <a href="#ai-processing">AI processing</a> above. Members of that workspace can read these documents and
        their facts, and so can the AI agents they connect to the workspace with API keys or MCP, just like anything else added
        there. So only import into a container whose members you&apos;re happy to share the message with. Only you can search
        Gmail with your connection, and only you, or an API key you created, can import with it.
      </p>

      <h3 id="google-security">How it&apos;s protected</h3>
      <ul>
        <li>
          Google&apos;s access and refresh tokens are encrypted with AES-256-GCM before they&apos;re saved, using a key only the
          server holds, and each one is tied to your connection so it can&apos;t be reused for anyone else.
        </li>
        <li>Connecting uses OAuth with PKCE and a short-lived, encrypted cookie.</li>
        <li>
          Recall&apos;s own logs never contain mail content. Errors record only which step failed and the HTTP status. The LLM
          gateway&apos;s request log does keep the text sent to the model, which includes imported mail, for 30 days (see{" "}
          <a href="#where-it-is-stored">Where it&apos;s stored</a>).
        </li>
      </ul>

      <h3 id="google-sharing">Sharing and limits</h3>
      <ul>
        <li>Google user data is never sold, never used for advertising and never used to build profiles for anyone else.</li>
        <li>
          It&apos;s only passed on where that&apos;s needed to provide the import you asked for (the AI processing step above,
          and the members and agents of the workspace you imported into), to keep the service secure, or when the law requires it.
        </li>
        <li>
          I don&apos;t read your Google data unless you ask me to look at specific messages (for example, to fix an import that
          failed), it&apos;s needed to investigate security problems or abuse, or the law requires it.
        </li>
        <li>Recall doesn&apos;t use Google user data to develop, improve or train generalized AI or machine learning models.</li>
      </ul>
      <p>
        Recall&apos;s use and transfer to any other app of information received from Google APIs will adhere to the{" "}
        <a href={LIMITED_USE_URL}>Google API Services User Data Policy</a>, including the Limited Use requirements.
      </p>

      <h3 id="google-disconnect">Disconnecting and deleting Gmail data</h3>
      <ul>
        <li>
          On the Connectors page, open the menu on the Gmail card and choose Disconnect. Recall deletes the stored tokens
          straight away and asks Google to revoke its access. If Google doesn&apos;t confirm, the page tells you, so you can
          remove Recall at Google yourself.
        </li>
        <li>
          You can also remove Recall from your Google account at <a href={GOOGLE_PERMISSIONS_URL}>myaccount.google.com/permissions</a>.
          Leaving a workspace, or being removed from it, deletes your connection there too, but doesn&apos;t revoke it at
          Google, so remove Recall there as well.
        </li>
        <li>
          Messages you already imported stay after you disconnect, until they&apos;re deleted. The email and each of its
          attachments are separate documents, and deleting one removes its stored text or file. Facts they produced are
          handled separately: deleting a fact on the Memories page hides it from search and agents, but keeps it in that
          fact&apos;s version history. To have every trace erased for good, write to <ContactEmail /> and I&apos;ll do it within
          30 days.
        </li>
      </ul>

      <h2 id="who-can-see-it">Who can see your data</h2>
      <ul>
        <li>
          Members of a workspace can see the content and facts in that workspace. Owners and admins also manage its members and
          API keys. An API key only ever sees its own workspace.
        </li>
        <li>
          I run the server, so I can technically reach the database. I only do that to run, fix or secure Recall, or when you ask
          me to, and for Google user data only in the cases listed under <a href="#google-sharing">Sharing and limits</a>.
        </li>
        <li>
          Services that handle data on Recall&apos;s behalf: Cloudflare (network, this site and backup storage), Anthropic (AI
          processing) and Google (only if you connect Gmail). No one else.
        </li>
      </ul>

      <h2 id="retention">How long it&apos;s kept</h2>
      <ul>
        <li>Your account and content: until they&apos;re deleted. Edited or deleted facts stay in their version history until I erase them on request.</li>
        <li>Imported Gmail messages: like any other content, they stay after you disconnect until they&apos;re deleted.</li>
        <li>Sessions: until you sign out, or 7 days without use.</li>
        <li>
          Gmail tokens: until you disconnect, connect again, leave the workspace or your account is deleted. If Google ends the
          grant first, the dead tokens stay encrypted until one of those happens.
        </li>
        <li>The LLM gateway&apos;s request log: 30 days.</li>
        <li>Backups: 30 days. Anything erased from Recall is gone from every backup 30 days later.</li>
      </ul>

      <h2 id="your-choices">Your choices and deletion requests</h2>
      <p>
        In the dashboard you can edit or delete facts (old versions stay in their history), delete documents, revoke API keys
        and disconnect Gmail. For anything
        else, such as a copy of your data, a correction, or deleting your account and everything in it, write to{" "}
        <ContactEmail />. I&apos;ll reply within 30 days and confirm once it&apos;s done.
      </p>

      <h2 id="security">Security</h2>
      <p>
        Recall is only reachable over HTTPS. Passwords and API keys are stored as hashes, Google tokens are encrypted, and
        backups sit in a private bucket. No system is perfectly secure, so if I learn of a breach that affects your data, I&apos;ll
        tell you.
      </p>

      <h2 id="children">Children</h2>
      <p>Recall isn&apos;t meant for anyone under 13, and I don&apos;t knowingly invite them.</p>

      <h2 id="changes">Changes to this policy</h2>
      <p>
        When this policy changes, I&apos;ll update this page and the date at the top. If a change affects how Google user data is
        used, I&apos;ll tell everyone who has connected Gmail first, and ask again where Google requires it. See also the{" "}
        <Link href="/terms">terms of service</Link>.
      </p>

      <h2 id="contact">Contact</h2>
      <p>
        Questions, requests or concerns about your data: <ContactEmail />.
      </p>
    </LegalPage>
  )
}
