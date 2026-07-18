import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { Modal } from "../components/ui";

export function WebhookSecretDialog({
  secret,
  endpoint,
  onClose,
}: {
  secret: string;
  endpoint?: string;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [completionCopied, setCompletionCopied] = useState(false);
  const notificationCenterSourceKey = sourceKeyFromNotificationCenterEndpoint(endpoint || "");
  const notificationCenterCompletion = notificationCenterSourceKey
    ? JSON.stringify({ signingSecret: secret, enabled: true }, null, 2)
    : "";

  async function copySecret() {
    await navigator.clipboard.writeText(secret);
    setCopied(true);
  }

  async function copyNotificationCenterCompletion() {
    await navigator.clipboard.writeText(notificationCenterCompletion);
    setCompletionCopied(true);
  }

  return (
    <Modal
      title="Save the signing secret"
      subtitle="Windforce will not show this value again after this dialog is closed."
      onClose={onClose}
      id="webhookSecretDialog"
    >
      <div className="secretReveal">
        <code id="webhookSigningSecret">{secret}</code>
        <button className="button" type="button" onClick={copySecret}>
          {copied ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}
          {copied ? "Copied" : "Copy secret"}
        </button>
      </div>
      <div className="inlineNotice warning">
        Configure the receiving service with this secret to verify the <span className="mono">X-Windforce-Signature</span> header.
      </div>
      {notificationCenterSourceKey ? (
        <div className="notificationCenterHandoff">
          <div>
            <strong>Notification Center source</strong>
            <p>
              Open source <span className="mono">{notificationCenterSourceKey}</span> and paste this body into the signing secret completion request.
            </p>
            <code>POST /api/v1/admin/sources/{notificationCenterSourceKey}/signing-secret</code>
          </div>
          <pre>{notificationCenterCompletion}</pre>
          <button className="button" type="button" onClick={copyNotificationCenterCompletion}>
            {completionCopied ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}
            {completionCopied ? "Copied" : "Copy Notification Center body"}
          </button>
        </div>
      ) : null}
      <footer className="dialogFooter webhookSecretFooter">
        <span className="fieldHint">Closing this dialog does not disable the webhook.</span>
        <button className="button primary" type="button" onClick={onClose}>
          I saved the secret
        </button>
      </footer>
    </Modal>
  );
}

function sourceKeyFromNotificationCenterEndpoint(endpoint: string): string {
  try {
    const url = new URL(endpoint);
    const match = url.pathname.match(/\/api\/v1\/ingress\/([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)$/);
    return match?.[1] || "";
  } catch {
    return "";
  }
}
