<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

import { fetchGraph } from "../api/graph";
import type { GraphNode, GraphResponse } from "../api/types";

interface Point {
  x: number;
  y: number;
}

interface DragState {
  nodeName: string;
  pointerId: number;
  offsetX: number;
  offsetY: number;
}

interface NormalizedNode {
  name: string;
  kind: string;
  endpoints: string[];
  depends: string[];
}

interface GraphEdge {
  key: string;
  from: string;
  to: string;
}

interface PositionedNode extends NormalizedNode {
  x: number;
  y: number;
  width: number;
  height: number;
}

type GraphLayer = "reverseProxy" | "application" | "other";

const loading = ref(false);
const loadingError = ref("");
const exportError = ref("");
const exportingImage = ref(false);
const graph = ref<GraphResponse | null>(null);
const positions = ref<Record<string, Point>>({});
const dragState = ref<DragState | null>(null);
const stageRef = ref<HTMLElement | null>(null);

const nodeWidth = 260;
const columnGap = 120;
const rowGap = 32;
const canvasPadding = 48;
const baseNodeHeight = 98;
const endpointLineHeight = 20;
const nodeNameLineHeight = 20;
const defaultCanvasWidth = 1040;
const defaultCanvasHeight = 640;
const stageInset = 16;
const nodeHorizontalPadding = 24;

const normalizedGraph = computed(() => normalizeGraph(graph.value));

const renderedNodes = computed<PositionedNode[]>(() =>
  normalizedGraph.value.nodes.map((node) => {
    const position = positions.value[node.name] ?? { x: canvasPadding, y: canvasPadding };

    return {
      ...node,
      x: position.x,
      y: position.y,
      width: nodeWidth,
      height: getNodeHeight(node),
    };
  }),
);

const renderedNodeMap = computed(
  () => new Map(renderedNodes.value.map((node) => [node.name, node])),
);

const canvasWidth = computed(() => {
  let width = defaultCanvasWidth;

  for (const node of renderedNodes.value) {
    width = Math.max(width, node.x + node.width + canvasPadding);
  }

  return width;
});

const canvasHeight = computed(() => {
  let height = defaultCanvasHeight;

  for (const node of renderedNodes.value) {
    height = Math.max(height, node.y + node.height + canvasPadding);
  }

  return height;
});

const renderedEdges = computed(() =>
  normalizedGraph.value.edges.flatMap((edge) => {
    const fromNode = renderedNodeMap.value.get(edge.from);
    const toNode = renderedNodeMap.value.get(edge.to);
    if (!fromNode || !toNode) {
      return [];
    }

    const startX = fromNode.x + fromNode.width;
    const startY = fromNode.y + fromNode.height / 2;
    const endX = toNode.x;
    const endY = toNode.y + toNode.height / 2;
    const curveOffset = Math.max(56, Math.abs(endX - startX) / 2);

    return [
      {
        ...edge,
        path: `M ${startX} ${startY} C ${startX + curveOffset} ${startY}, ${endX - curveOffset} ${endY}, ${endX} ${endY}`,
      },
    ];
  }),
);

const hasGraph = computed(() => renderedNodes.value.length > 0);
const graphNodeCount = computed(() => renderedNodes.value.length);
const graphEdgeCount = computed(() => renderedEdges.value.length);
const activeDraggedNodeName = computed(() => dragState.value?.nodeName ?? "");

async function loadGraph() {
  loading.value = true;
  loadingError.value = "";
  exportError.value = "";

  try {
    graph.value = await fetchGraph();
    resetLayout();
  } catch (error) {
    loadingError.value = error instanceof Error ? error.message : "Failed to load dependency graph";

    if (!graph.value) {
      positions.value = {};
    }
  } finally {
    loading.value = false;
  }
}

function resetLayout() {
  positions.value = buildLayout(normalizedGraph.value.nodes, normalizedGraph.value.edges);
}

async function downloadGraphImage() {
  if (!hasGraph.value || exportingImage.value) {
    return;
  }

  exportingImage.value = true;
  exportError.value = "";

  try {
    const svgMarkup = buildExportSvg(renderedNodes.value, renderedEdges.value, canvasWidth.value, canvasHeight.value);
    const svgBlob = new Blob([svgMarkup], { type: "image/svg+xml;charset=utf-8" });
    const svgUrl = URL.createObjectURL(svgBlob);

    try {
      const image = await loadImage(svgUrl);
      const scale = Math.max(2, Math.ceil(window.devicePixelRatio || 1));
      const canvas = document.createElement("canvas");
      canvas.width = Math.ceil(canvasWidth.value * scale);
      canvas.height = Math.ceil(canvasHeight.value * scale);

      const context = canvas.getContext("2d");
      if (!context) {
        throw new Error("Canvas context is unavailable");
      }

      context.scale(scale, scale);
      context.drawImage(image, 0, 0, canvasWidth.value, canvasHeight.value);

      const pngBlob = await canvasToBlob(canvas);
      triggerBlobDownload(pngBlob, buildDownloadFileName());
    } finally {
      URL.revokeObjectURL(svgUrl);
    }
  } catch (error) {
    exportError.value = error instanceof Error ? error.message : "Failed to export dependency graph";
  } finally {
    exportingImage.value = false;
  }
}

function onNodePointerDown(event: PointerEvent, nodeName: string) {
  if (event.button !== 0) {
    return;
  }

  const point = resolveStagePoint(event);
  const node = renderedNodeMap.value.get(nodeName);
  if (!point || !node) {
    return;
  }

  event.preventDefault();

  dragState.value = {
    nodeName,
    pointerId: event.pointerId,
    offsetX: point.x - node.x,
    offsetY: point.y - node.y,
  };
}

function handleWindowPointerMove(event: PointerEvent) {
  const currentDrag = dragState.value;
  if (!currentDrag || currentDrag.pointerId !== event.pointerId) {
    return;
  }

  const point = resolveStagePoint(event);
  const node = renderedNodeMap.value.get(currentDrag.nodeName);
  if (!point || !node) {
    return;
  }

  positions.value = {
    ...positions.value,
    [currentDrag.nodeName]: {
      x: clamp(point.x - currentDrag.offsetX, stageInset, canvasWidth.value - node.width - stageInset),
      y: clamp(point.y - currentDrag.offsetY, stageInset, canvasHeight.value - node.height - stageInset),
    },
  };
}

function handleWindowPointerUp(event: PointerEvent) {
  if (dragState.value?.pointerId !== event.pointerId) {
    return;
  }

  dragState.value = null;
}

function resolveStagePoint(event: PointerEvent): Point | null {
  const stage = stageRef.value;
  if (!stage) {
    return null;
  }

  const rect = stage.getBoundingClientRect();

  return {
    x: event.clientX - rect.left,
    y: event.clientY - rect.top,
  };
}

function clamp(value: number, min: number, max: number): number {
  if (max <= min) {
    return min;
  }

  return Math.min(Math.max(value, min), max);
}

function getNodeHeight(node: NormalizedNode): number {
  const endpointRows = Math.max(node.endpoints.length, 1);
  const extraNameLines = Math.max(getNodeNameLines(node.name).length - 1, 0);

  return baseNodeHeight + endpointRows * endpointLineHeight + extraNameLines * nodeNameLineHeight;
}

function normalizeGraph(response: GraphResponse | null): { nodes: NormalizedNode[]; edges: GraphEdge[] } {
  const sourceNodes = Array.isArray(response?.nodes) ? response.nodes : [];
  const nodesByName = new Map<string, NormalizedNode>();
  const edges: GraphEdge[] = [];
  const edgeKeys = new Set<string>();

  for (const node of sourceNodes) {
    nodesByName.set(node.name, normalizeNode(node));
  }

  for (const node of sourceNodes) {
    for (const dependencyName of Array.isArray(node.depends) ? node.depends : []) {
      const normalizedDependencyName = String(dependencyName || "").trim();
      if (normalizedDependencyName.length === 0) {
        continue;
      }

      const key = `${node.name}->${normalizedDependencyName}`;
      if (edgeKeys.has(key)) {
        continue;
      }

      edges.push({
        key,
        from: node.name,
        to: normalizedDependencyName,
      });
      edgeKeys.add(key);
    }
  }

  return {
    nodes: Array.from(nodesByName.values()).sort((left, right) => left.name.localeCompare(right.name)),
    edges,
  };
}

function normalizeNode(node: GraphNode): NormalizedNode {
  return {
    name: String(node.name || "").trim(),
    kind: String(node.kind || "application").trim() || "application",
    endpoints: Array.isArray(node.endpoints) ? [...node.endpoints] : [],
    depends: Array.isArray(node.depends)
      ? node.depends
          .map((dependency) => String(dependency || "").trim())
          .filter((dependencyName) => dependencyName.length > 0)
      : [],
  };
}

function buildLayout(nodes: NormalizedNode[], _edges: GraphEdge[]): Record<string, Point> {
  const nodesByLayer = new Map<GraphLayer, NormalizedNode[]>();
  for (const node of nodes) {
    const layer = resolveLayer(node.kind);
    if (!nodesByLayer.has(layer)) {
      nodesByLayer.set(layer, []);
    }
    nodesByLayer.get(layer)?.push(node);
  }

  const nextPositions: Record<string, Point> = {};
  const layerOrder: GraphLayer[] = ["reverseProxy", "application", "other"];
  const layers = layerOrder.filter((layer) => nodesByLayer.has(layer));

  for (const [index, layer] of layers.entries()) {
    const layerNodes = [...(nodesByLayer.get(layer) ?? [])].sort((left, right) => left.name.localeCompare(right.name));

    let currentY = canvasPadding;
    const currentX = canvasPadding + index * (nodeWidth + columnGap);

    for (const node of layerNodes) {
      nextPositions[node.name] = {
        x: currentX,
        y: currentY,
      };
      currentY += getNodeHeight(node) + rowGap;
    }
  }

  return nextPositions;
}

function resolveLayer(kind: string): GraphLayer {
  switch (kind) {
    case "reverseProxy":
      return "reverseProxy";
    case "application":
      return "application";
    default:
      return "other";
  }
}

function formatNodeKind(kind: string): string {
  return kind
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .split(" ")
    .filter((word) => word.length > 0)
    .map((word) => word[0].toUpperCase() + word.slice(1))
    .join(" ");
}

function buildExportSvg(
  nodes: PositionedNode[],
  edges: Array<GraphEdge & { path: string }>,
  width: number,
  height: number,
): string {
  const edgeMarkup = edges
    .map(
      (edge) =>
        `<path d="${escapeXml(edge.path)}" class="graph-export-edge-path" marker-end="url(#graph-export-edge-arrow)" />`,
    )
    .join("");

  const nodeMarkup = nodes
    .map((node) => {
      const kindY = 28;
      const nameY = 56;
      const nameLines = getNodeNameLines(node.name);
      const endpointsY = 84 + (nameLines.length - 1) * nodeNameLineHeight;
      const endpointsMarkup =
        node.endpoints.length > 0
          ? node.endpoints
              .map((endpoint, index) => {
                const y = endpointsY + index * endpointLineHeight;

                return [
                  `<circle cx="18" cy="${y - 4}" r="2.5" class="graph-export-endpoint-dot" />`,
                  `<text x="28" y="${y}" class="graph-export-endpoint-text">${escapeXml(endpoint)}</text>`,
                ].join("");
              })
              .join("")
          : `<text x="14" y="${endpointsY}" class="graph-export-empty-text">No public endpoints</text>`;

      return [
        `<g transform="translate(${node.x} ${node.y})">`,
        `<rect width="${node.width}" height="${node.height}" rx="14" ry="14" class="graph-export-node-card" />`,
        `<text x="14" y="${kindY}" class="graph-export-node-kind">${escapeXml(formatNodeKind(node.kind))}</text>`,
        nameLines
          .map(
            (line, index) =>
              `<text x="14" y="${nameY + index * nodeNameLineHeight}" class="graph-export-node-name">${escapeXml(line)}</text>`,
          )
          .join(""),
        endpointsMarkup,
        `</g>`,
      ].join("");
    })
    .join("");

  return [
    `<?xml version="1.0" encoding="UTF-8"?>`,
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">`,
    `<defs>`,
    `<pattern id="graph-export-grid" width="32" height="32" patternUnits="userSpaceOnUse">`,
    `<path d="M 32 0 L 0 0 0 32" fill="none" stroke="rgba(161, 192, 204, 0.08)" stroke-width="1" />`,
    `</pattern>`,
    `<marker id="graph-export-edge-arrow" markerWidth="10" markerHeight="10" refX="8" refY="5" orient="auto" markerUnits="strokeWidth">`,
    `<path d="M 0 0 L 10 5 L 0 10 z" class="graph-export-edge-arrow" />`,
    `</marker>`,
    `<style>`,
    `text { font-family: "Space Grotesk", "Segoe UI", sans-serif; }`,
    `.graph-export-stage { fill: rgba(8, 21, 32, 0.55); }`,
    `.graph-export-grid { fill: url(#graph-export-grid); }`,
    `.graph-export-edge-path { fill: none; stroke: rgba(247, 178, 103, 0.72); stroke-width: 2; }`,
    `.graph-export-edge-arrow { fill: rgba(247, 178, 103, 0.82); }`,
    `.graph-export-node-card { fill: rgba(7, 17, 27, 0.96); stroke: rgba(130, 196, 214, 0.32); stroke-width: 1; }`,
    `.graph-export-node-kind { fill: #a1c0cc; font-size: 12px; letter-spacing: 0.8px; text-transform: uppercase; }`,
    `.graph-export-node-name { fill: #e3f4fb; font-size: 16px; font-weight: 700; }`,
    `.graph-export-endpoint-dot { fill: #a1c0cc; }`,
    `.graph-export-endpoint-text, .graph-export-empty-text { fill: #a1c0cc; font-size: 13px; }`,
    `</style>`,
    `</defs>`,
    `<rect width="${width}" height="${height}" rx="12" ry="12" class="graph-export-stage" />`,
    `<rect width="${width}" height="${height}" rx="12" ry="12" class="graph-export-grid" />`,
    edgeMarkup,
    nodeMarkup,
    `</svg>`,
  ].join("");
}

function escapeXml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error("Failed to render graph image"));
    image.src = url;
  });
}

function canvasToBlob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (!blob) {
        reject(new Error("Failed to build PNG image"));
        return;
      }

      resolve(blob);
    }, "image/png");
  });
}

function triggerBlobDownload(blob: Blob, fileName: string) {
  const link = document.createElement("a");
  const url = URL.createObjectURL(blob);

  try {
    link.href = url;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
  } finally {
    link.remove();
    URL.revokeObjectURL(url);
  }
}

function buildDownloadFileName(): string {
  const now = new Date();
  const year = now.getFullYear();
  const month = String(now.getMonth() + 1).padStart(2, "0");
  const day = String(now.getDate()).padStart(2, "0");
  const hours = String(now.getHours()).padStart(2, "0");
  const minutes = String(now.getMinutes()).padStart(2, "0");

  return `dependency-graph-${year}-${month}-${day}-${hours}${minutes}.png`;
}

function getNodeNameLines(name: string): string[] {
  return wrapText(name, nodeWidth - nodeHorizontalPadding, '700 16px "Space Grotesk", "Segoe UI", sans-serif');
}

function wrapText(text: string, maxWidth: number, font: string): string[] {
  const normalizedText = text.trim();
  if (normalizedText.length === 0) {
    return [""];
  }

  const lines: string[] = [];
  let currentLine = "";

  for (const character of Array.from(normalizedText)) {
    const nextLine = `${currentLine}${character}`;
    if (currentLine.length > 0 && measureTextWidth(nextLine, font) > maxWidth) {
      lines.push(currentLine);
      currentLine = character;
      continue;
    }

    currentLine = nextLine;
  }

  if (currentLine.length > 0) {
    lines.push(currentLine);
  }

  return lines;
}

function measureTextWidth(text: string, font: string): number {
  if (typeof document === "undefined") {
    return text.length * 9;
  }

  const canvas = document.createElement("canvas");
  const context = canvas.getContext("2d");
  if (!context) {
    return text.length * 9;
  }

  context.font = font;

  return context.measureText(text).width;
}

onMounted(() => {
  window.addEventListener("pointermove", handleWindowPointerMove);
  window.addEventListener("pointerup", handleWindowPointerUp);
  void loadGraph();
});

onBeforeUnmount(() => {
  window.removeEventListener("pointermove", handleWindowPointerMove);
  window.removeEventListener("pointerup", handleWindowPointerUp);
});
</script>

<template>
  <section class="services-page graph-page">
    <header class="services-header graph-header">
      <div>
        <h2>Dependency Graph</h2>
        <p class="meta">Drag nodes to inspect service dependencies built from collected metadata.</p>
      </div>

      <div class="graph-actions">
        <button type="button" class="button-ghost graph-action-button" :disabled="!hasGraph" @click="resetLayout">
          Reset layout
        </button>
        <button
          type="button"
          class="button-ghost graph-action-button"
          :disabled="!hasGraph || exportingImage"
          @click="downloadGraphImage"
        >
          {{ exportingImage ? "Downloading..." : "Download PNG" }}
        </button>
        <button type="button" class="graph-action-button" :disabled="loading" @click="loadGraph">
          {{ loading ? "Loading..." : "Reload" }}
        </button>
      </div>
    </header>

    <section class="graph-summary-panel">
      <div class="graph-summary-chip">
        <strong>{{ graphNodeCount }}</strong>
        <span>nodes</span>
      </div>
      <div class="graph-summary-chip">
        <strong>{{ graphEdgeCount }}</strong>
        <span>edges</span>
      </div>
      <p v-if="loadingError" class="meta graph-inline-state">Failed to load graph: {{ loadingError }}</p>
      <p v-else-if="exportError" class="meta graph-inline-state">Failed to export graph: {{ exportError }}</p>
      <p v-else class="meta graph-inline-state">Dependencies are inferred from service environment variables and web routes.</p>
    </section>

    <div v-if="loading && !hasGraph" class="services-empty">
      <p class="meta">Loading dependency graph...</p>
    </div>

    <div v-else-if="!hasGraph" class="services-empty">
      <p class="meta">No services available to build a dependency graph.</p>
    </div>

    <section v-else class="graph-canvas-card">
      <div class="graph-canvas-scroll">
        <div
          ref="stageRef"
          class="graph-stage"
          :style="{
            width: `${canvasWidth}px`,
            height: `${canvasHeight}px`,
          }"
        >
          <svg
            class="graph-edges-layer"
            :width="canvasWidth"
            :height="canvasHeight"
            :viewBox="`0 0 ${canvasWidth} ${canvasHeight}`"
            aria-hidden="true"
          >
            <defs>
              <marker
                id="graph-edge-arrow"
                markerWidth="10"
                markerHeight="10"
                refX="8"
                refY="5"
                orient="auto"
                markerUnits="strokeWidth"
              >
                <path d="M 0 0 L 10 5 L 0 10 z" class="graph-edge-arrow" />
              </marker>
            </defs>

            <path
              v-for="edge in renderedEdges"
              :key="edge.key"
              :d="edge.path"
              class="graph-edge-path"
              marker-end="url(#graph-edge-arrow)"
            />
          </svg>

          <article
            v-for="node in renderedNodes"
            :key="node.name"
            class="graph-node-card"
            :class="{ dragging: activeDraggedNodeName === node.name }"
            :style="{
              width: `${node.width}px`,
              height: `${node.height}px`,
              transform: `translate(${node.x}px, ${node.y}px)`,
            }"
            @pointerdown="onNodePointerDown($event, node.name)"
          >
            <header class="graph-node-header">
              <span class="graph-node-kind">{{ formatNodeKind(node.kind) }}</span>
            </header>

            <h3 class="graph-node-name">{{ node.name }}</h3>

            <ul v-if="node.endpoints.length > 0" class="graph-node-endpoints">
              <li v-for="endpoint in node.endpoints" :key="endpoint">
                {{ endpoint }}
              </li>
            </ul>

            <p v-else class="graph-node-empty">No public endpoints</p>
          </article>
        </div>
      </div>
    </section>
  </section>
</template>

<style scoped>
.graph-page {
  gap: 14px;
}

.graph-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 14px;
}

.graph-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.graph-action-button:disabled {
  cursor: not-allowed;
  opacity: 0.7;
}

.graph-summary-panel {
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--card);
  padding: 12px;
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
}

.graph-summary-chip {
  min-width: 88px;
  border: 1px solid rgba(161, 192, 204, 0.24);
  border-radius: 12px;
  background: rgba(9, 18, 28, 0.78);
  padding: 8px 10px;
  display: grid;
  gap: 2px;
}

.graph-summary-chip strong {
  font-size: 1rem;
}

.graph-summary-chip span {
  color: var(--muted);
  font-size: 0.8rem;
}

.graph-inline-state {
  flex: 1 1 220px;
}

.graph-canvas-card {
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--card);
  overflow: hidden;
}

.graph-canvas-scroll {
  overflow: auto;
  padding: 12px;
}

.graph-stage {
  position: relative;
  border-radius: 12px;
  background:
    linear-gradient(rgba(161, 192, 204, 0.04) 1px, transparent 1px),
    linear-gradient(90deg, rgba(161, 192, 204, 0.04) 1px, transparent 1px),
    rgba(8, 21, 32, 0.55);
  background-size: 32px 32px;
  overflow: hidden;
}

.graph-edges-layer {
  position: absolute;
  inset: 0;
}

.graph-edge-path {
  fill: none;
  stroke: rgba(247, 178, 103, 0.72);
  stroke-width: 2;
}

.graph-edge-arrow {
  fill: rgba(247, 178, 103, 0.82);
}

.graph-node-card {
  position: absolute;
  top: 0;
  left: 0;
  border: 1px solid rgba(130, 196, 214, 0.32);
  border-radius: 14px;
  background: rgba(7, 17, 27, 0.96);
  backdrop-filter: blur(8px);
  padding: 12px;
  display: grid;
  gap: 10px;
  box-shadow: 0 16px 36px rgba(0, 0, 0, 0.22);
  cursor: grab;
  touch-action: none;
  user-select: none;
}

.graph-node-card.dragging {
  cursor: grabbing;
  border-color: rgba(247, 178, 103, 0.72);
  box-shadow: 0 20px 44px rgba(0, 0, 0, 0.3);
}

.graph-node-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
}

.graph-node-kind {
  font-size: 0.75rem;
  color: var(--muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.graph-node-name {
  margin: 0;
  font-size: 1rem;
  line-height: 1.25;
  overflow-wrap: anywhere;
}

.graph-node-endpoints {
  margin: 0;
  padding-left: 18px;
  display: grid;
  gap: 4px;
  color: var(--muted);
  font-size: 0.84rem;
}

.graph-node-empty {
  margin: 0;
  color: var(--muted);
  font-size: 0.84rem;
}

@media (max-width: 860px) {
  .graph-header {
    flex-direction: column;
    align-items: stretch;
  }

  .graph-actions {
    justify-content: flex-start;
  }
}
</style>
