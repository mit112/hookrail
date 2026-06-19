import { useState } from "react";

interface SecretRevealProps {
  secret: string;
  onClose: () => void;
}

export function SecretReveal({ secret, onClose }: SecretRevealProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
    } catch {
      // clipboard API may be unavailable (e.g. non-HTTPS context)
    }
  };

  return (
    <div role="dialog">
      <h2>Secret</h2>
      <p>{secret}</p>
      <p>
        <strong>This secret will not be shown again. Save it now.</strong>
      </p>
      <button type="button" onClick={handleCopy}>
        {copied ? "Copied" : "Copy"}
      </button>
      <button type="button" onClick={onClose}>
        I saved it
      </button>
    </div>
  );
}
