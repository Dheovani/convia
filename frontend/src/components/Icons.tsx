import type { ReactNode } from "react";

function Icon({ children }: { children: ReactNode }) {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      width="18"
      height="18"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {children}
    </svg>
  );
}

export function AddReactionIcon() {
  return (
    <Icon>
      <path d="M21 12a9 9 0 1 1-9-9" />
      <path d="M8 14s1.5 2 4 2 4-2 4-2" />
      <path d="M9 9h.01M15 9h.01" />
      <path d="M19 2v6M16 5h6" />
    </Icon>
  );
}

export function ReplyIcon() {
  return (
    <Icon>
      <path d="M9 14 4 9l5-5" />
      <path d="M4 9h10a6 6 0 0 1 6 6v5" />
    </Icon>
  );
}

export function EditIcon() {
  return (
    <Icon>
      <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </Icon>
  );
}

export function DeleteIcon() {
  return (
    <Icon>
      <path d="M3 6h18" />
      <path d="M8 6V4h8v2" />
      <path d="M19 6l-1 14H6L5 6" />
      <path d="M10 11v6M14 11v6" />
    </Icon>
  );
}

export function PhoneIcon() {
  return (
    <Icon>
      <path d="M5 4h4l2 5-2.5 1.5a11 11 0 0 0 5 5L15 13l5 2v4a2 2 0 0 1-2 2A16 16 0 0 1 3 6a2 2 0 0 1 2-2Z" />
    </Icon>
  );
}

export function VideoIcon() {
  return (
    <Icon>
      <rect x="2" y="6" width="14" height="12" rx="2" />
      <path d="m16 10 6-3v10l-6-3Z" />
    </Icon>
  );
}

// IncomingIcon and OutgoingIcon say which way a call went, in the call log.
export function IncomingIcon() {
  return (
    <Icon>
      <path d="M17 7 7 17" />
      <path d="M15 17H7V9" />
    </Icon>
  );
}

export function OutgoingIcon() {
  return (
    <Icon>
      <path d="m7 17 10-10" />
      <path d="M9 7h8v8" />
    </Icon>
  );
}
