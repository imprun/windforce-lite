import SwaggerUI from "swagger-ui-react";
import "swagger-ui-react/swagger-ui.css";
import { appOpenAPIURL } from "../lib/api";

export function OpenAPIViewerPage({ workspace, appKey }: { workspace: string; appKey: string }) {
  const documentURL = appOpenAPIURL(workspace, appKey);
  return (
    <main className="openAPIPublicPage">
      <header className="openAPIPageHeader">
        <div>
          <p className="eyebrow">OpenAPI reference</p>
          <h1>{appKey} API</h1>
          <p>Published from the active release.</p>
        </div>
        <a className="button" href={documentURL} target="_blank" rel="noreferrer">
          OpenAPI JSON
        </a>
      </header>
      <section className="openAPIReference" aria-label={`${appKey} OpenAPI reference`}>
        <SwaggerUI url={documentURL} deepLinking docExpansion="list" defaultModelsExpandDepth={-1} persistAuthorization={false} />
      </section>
    </main>
  );
}
