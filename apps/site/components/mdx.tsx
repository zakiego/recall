import { Step, Steps } from "fumadocs-ui/components/steps"
import { Tab, Tabs } from "fumadocs-ui/components/tabs"
import defaultMdxComponents from "fumadocs-ui/mdx"
import type { MDXComponents } from "mdx/types"
import {
  AppSyncDiagram,
  ArchitectureDiagram,
  AskDiagram,
  ConceptsDiagram,
  DeployDiagram,
  PipelineDiagram,
} from "@/components/diagram/docs-diagrams"

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    AppSyncDiagram,
    ArchitectureDiagram,
    AskDiagram,
    ConceptsDiagram,
    DeployDiagram,
    PipelineDiagram,
    Step,
    Steps,
    Tab,
    Tabs,
    ...components,
  } satisfies MDXComponents
}

export const useMDXComponents = getMDXComponents

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>
}
