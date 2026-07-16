import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { Modal } from "../components/ui";

export function WebhookSecretDialog({
  secret,
  onClose,
}: {
  secret: string;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);

  async function copySecret() {
    await navigator.clipboard.writeText(secret);
    setCopied(true);
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
      <footer className="dialogFooter webhookSecretFooter">
        <span className="fieldHint">Closing this dialog does not disable the webhook.</span>
        <button className="button primary" type="button" onClick={onClose}>
          I saved the secret
        </button>
      </footer>
    </Modal>
  );
}
