// The dashboard's entry: a client-only single-page app under /app. The Go
// server answers /api, /v1, /mcp and /health, and serves dist/index.html for
// every /app path, so routing happens here.
import "@/styles/app.css"
import { RouterProvider } from "@tanstack/react-router"
import { createRoot } from "react-dom/client"
import { getRouter } from "./router"

const router = getRouter()

createRoot(document.getElementById("root")!).render(<RouterProvider router={router} />)
