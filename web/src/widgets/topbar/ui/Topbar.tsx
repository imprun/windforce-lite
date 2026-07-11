"use client";

type Props = {
  title: string;
  subtitle: string;
};

export function Topbar({ title, subtitle }: Props) {
  return (
    <header className="topbar">
      <div className="pageTitle">
        <h1>{title}</h1>
        <p>{subtitle}</p>
      </div>
    </header>
  );
}
