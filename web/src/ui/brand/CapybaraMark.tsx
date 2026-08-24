import {useId} from "react";

export function CapybaraMark({
  className,
  size = "compact"
}: {
  className?: string;
  size?: "small" | "compact" | "hero";
}) {
  const maskID = `capybara-${useId().replaceAll(":", "")}`;
  const dimensions = size === "hero"
    ? {width: 34, height: 26}
    : size === "small"
      ? {width: 16, height: 12}
      : {width: 24, height: 18};
  return (
    <svg
      aria-hidden="true"
      className={className}
      viewBox="0 0 32 24"
      width={dimensions.width}
      height={dimensions.height}
      fill="none"
    >
      <defs>
        <mask id={maskID}>
          <rect width="32" height="24" fill="white" />
          <circle cx="25.1" cy="9.2" r="1.15" fill="black" />
        </mask>
      </defs>
      <path
        fill="currentColor"
        mask={`url(#${maskID})`}
        d="M3.2 10.2c0-3.7 2.9-6.7 6.6-6.7h8.8c1.1-1.7 3.4-2.3 5.1-1.1.7.5 1.1 1.2 1.3 2 2.4.4 4.1 2.4 4.1 4.9v4.2c0 1.5-1.2 2.7-2.7 2.7h-2.1v3.1c0 1-.8 1.7-1.7 1.7h-1.1c-.7 0-1.3-.4-1.6-1l-1.3-2.7H10l-1.2 2.6c-.3.7-1 1.1-1.7 1.1H6c-1 0-1.7-.8-1.7-1.7v-3.8c-.7-1.1-1.1-2.4-1.1-3.8v-1.5Zm21.4-4.7c-.6-.8-1.6-1.1-2.5-.8l1.6 1.5.9-.7Zm3.1 5.7h2c.6 0 1.1.5 1.1 1.1v.5c0 1.1-.9 2-2 2h-1.1v-3.6Z"
      />
    </svg>
  );
}
