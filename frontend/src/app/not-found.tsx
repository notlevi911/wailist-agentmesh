import Link from "next/link";

const NOT_FOUND_CSS = `
.not-found-container {
  min-height: 100vh;
  min-height: 100dvh;
}
`;

export default function NotFound() {
  return (
    <div className="not-found-container" style={containerStyle}>
      <style>{NOT_FOUND_CSS}</style>
      {/* Subtle glow background effect */}
      <div style={glowBgStyle} />
      
      {/* SVG canvas grid background element */}
      <div className="canvas-bg" style={gridBgStyle} />

      {/* Main card panel */}
      <div className="reveal" style={cardStyle}>
        {/* AgentMesh logo mark */}
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src="/logo.png"
          alt="AgentMesh"
          width={120}
          height={120}
          style={logoMarkStyle}
        />

        {/* 404 error code */}
        <div style={errorCodeStyle}>404</div>

        {/* Heading */}
        <h1 style={headingStyle}>Lost in the Mesh</h1>

        {/* Description */}
        <p style={descriptionStyle}>
          This route wandered off mid workflow and never checked back in.
          Your wallet and credits are exactly where you left them, this URL
          just isn&apos;t a real node.
        </p>

        {/* Action navigation links */}
        <div style={actionsStyle}>
          <Link href="/workflows" style={primaryButtonStyle}>
            Go to Workflows
          </Link>
          <Link href="/" style={secondaryButtonStyle}>
            Return Home
          </Link>
        </div>
      </div>
    </div>
  );
}

const containerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  alignItems: "center",
  justifyContent: "center",
  background: "var(--bg)",
  color: "var(--fg)",
  fontFamily: "var(--font-sans)",
  position: "relative",
  overflow: "hidden",
  padding: "24px",
};

const glowBgStyle: React.CSSProperties = {
  position: "absolute",
  width: "400px",
  height: "400px",
  background: "var(--accent-glow)",
  filter: "blur(120px)",
  borderRadius: "50%",
  top: "50%",
  left: "50%",
  transform: "translate(-50%, -50%)",
  pointerEvents: "none",
  opacity: 0.15,
};

const gridBgStyle: React.CSSProperties = {
  position: "absolute",
  inset: 0,
  backgroundSize: "20px 20px",
  opacity: 0.1,
  pointerEvents: "none",
};

const cardStyle: React.CSSProperties = {
  background: "var(--bg-elev-1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--r-4)",
  padding: "48px 32px",
  maxWidth: "460px",
  width: "100%",
  textAlign: "center",
  boxShadow: "0 20px 48px rgba(0, 0, 0, 0.6)",
  zIndex: 1,
  display: "flex",
  flexDirection: "column",
  alignItems: "center",
};

const logoMarkStyle: React.CSSProperties = {
  marginBottom: "24px",
  animation: "float-y 6s ease-in-out infinite",
  borderRadius: "22%",
};

const errorCodeStyle: React.CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: "72px",
  fontWeight: 700,
  letterSpacing: "-0.04em",
  lineHeight: 1,
  color: "var(--danger)",
  marginBottom: "8px",
};

const headingStyle: React.CSSProperties = {
  fontSize: "32px",
  fontWeight: 600,
  letterSpacing: "-0.02em",
  margin: "0 0 12px 0",
};

const descriptionStyle: React.CSSProperties = {
  fontSize: "14px",
  lineHeight: "1.6",
  color: "var(--fg-muted)",
  margin: "0 0 32px 0",
};

const actionsStyle: React.CSSProperties = {
  display: "flex",
  gap: "12px",
  width: "100%",
};

const primaryButtonStyle: React.CSSProperties = {
  flex: 1,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  height: "40px",
  background: "var(--accent)",
  color: "var(--accent-fg)",
  border: "none",
  borderRadius: "var(--r-2)",
  fontSize: "13.5px",
  fontWeight: 600,
  textDecoration: "none",
  transition: "background 0.15s var(--ease)",
  cursor: "pointer",
};

const secondaryButtonStyle: React.CSSProperties = {
  flex: 1,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  height: "40px",
  background: "transparent",
  color: "var(--fg)",
  border: "1px solid var(--border-strong)",
  borderRadius: "var(--r-2)",
  fontSize: "13.5px",
  fontWeight: 600,
  textDecoration: "none",
  transition: "background 0.15s var(--ease), border-color 0.15s var(--ease)",
  cursor: "pointer",
};


