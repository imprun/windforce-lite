import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "windforce-lite",
  description: "windforce-lite deployment control plane",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
