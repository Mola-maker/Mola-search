import * as THREE from 'three';

export interface YuanbaoScene {
  setStage(stage: string): void;
  setAgentCount?(n: number): void;
  dispose(): void;
}

type Stage =
  | 'idle'
  | 'planning'
  | 'searching'
  | 'analyzing'
  | 'synthesizing'
  | 'complete'
  | 'error'
  | 'cancelled';

type Agent = {
  group: THREE.Group;
  model: THREE.Group;
  role: string;
  accent: THREE.Color;
  position: THREE.Vector3;
  velocity: THREE.Vector3;
  target: THREE.Vector3;
  home: THREE.Vector3;
  squash: number;
  squashVelocity: number;
  contact: number;
  labelCanvas: HTMLCanvasElement;
  labelTexture: THREE.CanvasTexture;
  faceCanvas: HTMLCanvasElement;
  faceTexture: THREE.CanvasTexture;
  lastFaceUpdate: number;
};

const MAX_AGENTS = 8;
const MIN_AGENTS = 4;
const STAGES = new Set<Stage>([
  'idle',
  'planning',
  'searching',
  'analyzing',
  'synthesizing',
  'complete',
  'error',
  'cancelled',
]);
const ROLE_COLORS = ['#55b8ff', '#55e6c1', '#a98bff', '#ffd166'];
const STAGE_LABELS: Record<Stage, string> = {
  idle: 'READY',
  planning: 'PLANNING',
  searching: 'SEARCHING',
  analyzing: 'ANALYZING',
  synthesizing: 'SYNTHESIZING',
  complete: 'COMPLETE',
  error: 'ERROR',
  cancelled: 'CANCELLED',
};

function normalizeStage(stage: string): Stage {
  const value = stage.toLowerCase() as Stage;
  return STAGES.has(value) ? value : 'idle';
}

function roleAt(index: number, count: number): string {
  if (index === 0) return 'Planner';
  if (index === count - 2) return 'Analyst';
  if (index === count - 1) return 'Synthesizer';
  return `Searcher ${index}`;
}

function accentAt(index: number, count: number): THREE.Color {
  const role = roleAt(index, count);
  const color = role === 'Planner'
    ? ROLE_COLORS[0]
    : role === 'Analyst'
      ? ROLE_COLORS[2]
      : role === 'Synthesizer'
        ? ROLE_COLORS[3]
        : ROLE_COLORS[1];
  return new THREE.Color(color);
}

function roundedRect(
  context: CanvasRenderingContext2D,
  x: number,
  y: number,
  width: number,
  height: number,
  radius: number,
): void {
  context.beginPath();
  context.roundRect(x, y, width, height, radius);
}

type EyeKind = 'open' | 'wink' | 'happy' | 'calm';

function drawEye(
  context: CanvasRenderingContext2D,
  x: number,
  y: number,
  kind: EyeKind,
  openness = 1,
  lookX = 0,
  lookY = 0,
): void {
  context.save();
  context.translate(x, y);
  context.fillStyle = '#06251b';
  context.strokeStyle = '#06251b';
  context.lineWidth = 13;
  context.lineCap = 'round';
  context.lineJoin = 'round';
  context.beginPath();
  if (kind === 'wink') {
    context.moveTo(17, -24);
    context.lineTo(-15, 0);
    context.lineTo(17, 24);
    context.stroke();
  } else if (kind === 'happy') {
    context.arc(0, 9, 24, Math.PI * 1.12, Math.PI * 1.88);
    context.stroke();
  } else if (kind === 'calm') {
    context.moveTo(-20, 0);
    context.quadraticCurveTo(0, 8, 20, 0);
    context.stroke();
  } else {
    context.ellipse(lookX, lookY, 17, Math.max(3, 34 * openness), 0, 0, Math.PI * 2);
    context.fill();
  }
  context.restore();
}

function drawFace(agent: Agent, stage: Stage, time: number): void {
  const context = agent.faceCanvas.getContext('2d');
  if (!context) return;
  const size = agent.faceCanvas.width;
  const phase = time + agent.role.length * 0.37;
  const active = stage === 'searching' || stage === 'analyzing' || stage === 'synthesizing';
  const energy = stage === 'searching' ? 1 : active ? 0.55 : 0.16;
  const flow = energy * (0.032 + agent.contact * 0.055);
  context.clearRect(0, 0, size, size);

  const points: THREE.Vector2[] = [];
  const pointCount = 64;
  for (let index = 0; index < pointCount; index += 1) {
    const angle = (index / pointCount) * Math.PI * 2;
    const ripple = 1
      + Math.sin(angle * 3 + phase * 2.6) * flow
      + Math.sin(angle * 5 - phase * 1.8) * flow * 0.45;
    const asymmetricTop = Math.sin(angle) < 0 ? 1 + Math.cos(angle + 0.5) * 0.09 : 1;
    points.push(new THREE.Vector2(
      256 + Math.cos(angle + Math.sin(phase) * flow * 0.3) * 98 * ripple,
      268 + Math.sin(angle) * 94 * ripple * asymmetricTop,
    ));
  }
  const firstMidpoint = points[0].clone().add(points[pointCount - 1]).multiplyScalar(0.5);
  context.beginPath();
  context.moveTo(firstMidpoint.x, firstMidpoint.y);
  for (let index = 0; index < pointCount; index += 1) {
    const point = points[index];
    const next = points[(index + 1) % pointCount];
    context.quadraticCurveTo(point.x, point.y, (point.x + next.x) / 2, (point.y + next.y) / 2);
  }
  context.closePath();
  const white = context.createRadialGradient(218, 215, 12, 256, 268, 180);
  white.addColorStop(0, 'rgba(255,255,255,.99)');
  white.addColorStop(0.7, 'rgba(247,255,252,.97)');
  white.addColorStop(1, 'rgba(205,247,229,.94)');
  context.fillStyle = white;
  context.shadowColor = 'rgba(0,60,35,.24)';
  context.shadowBlur = 16;
  context.fill();
  context.shadowBlur = 0;

  const blink = stage === 'searching' && Math.sin(phase * 5.4) > 0.76 ? 0.1 : 1;
  let mouth = 'idle';
  if (stage === 'planning') {
    drawEye(context, 207, 250, 'open', 0.88, 5, -5);
    drawEye(context, 307, 253, 'open', 0.62, 4, -3);
    context.strokeStyle = '#06251b';
    context.lineWidth = 9;
    context.lineCap = 'round';
    context.beginPath();
    context.moveTo(283, 207);
    context.quadraticCurveTo(309, 194, 332, 208);
    context.stroke();
    mouth = 'thinking';
  } else if (stage === 'searching') {
    drawEye(context, 207, 250, 'open', blink * 1.08);
    drawEye(context, 307, 250, 'wink');
    mouth = 'excited';
  } else if (stage === 'analyzing') {
    drawEye(context, 207, 252, 'open', 0.52);
    drawEye(context, 307, 252, 'open', 0.52);
    context.strokeStyle = '#06251b';
    context.lineWidth = 9;
    context.lineCap = 'round';
    context.beginPath();
    context.moveTo(183, 214);
    context.lineTo(226, 224);
    context.moveTo(286, 224);
    context.lineTo(329, 214);
    context.stroke();
    mouth = 'focused';
  } else if (stage === 'synthesizing') {
    drawEye(context, 207, 250, 'happy');
    drawEye(context, 307, 250, 'wink');
    mouth = 'smile';
  } else if (stage === 'complete') {
    drawEye(context, 207, 250, 'happy');
    drawEye(context, 307, 250, 'happy');
    mouth = 'complete';
  } else if (stage === 'error') {
    drawEye(context, 207, 254, 'open', 0.8, 0, 3);
    drawEye(context, 307, 254, 'open', 0.8, 0, 3);
    context.strokeStyle = '#06251b';
    context.lineWidth = 9;
    context.lineCap = 'round';
    context.beginPath();
    context.moveTo(183, 220);
    context.lineTo(225, 207);
    context.moveTo(289, 207);
    context.lineTo(331, 220);
    context.stroke();
    mouth = 'worried';
  } else if (stage === 'cancelled') {
    drawEye(context, 207, 252, 'calm');
    drawEye(context, 307, 252, 'calm');
    mouth = 'calm';
  } else {
    drawEye(context, 207, 250, 'open');
    drawEye(context, 307, 250, 'wink');
  }

  context.strokeStyle = '#06251b';
  context.fillStyle = '#06251b';
  context.lineWidth = 12;
  context.lineCap = 'round';
  context.beginPath();
  if (mouth === 'complete' || mouth === 'excited') {
    context.ellipse(256, 316, mouth === 'complete' ? 35 : 25, mouth === 'complete' ? 27 : 20, 0, 0, Math.PI * 2);
    context.fill();
    context.fillStyle = '#8de8c1';
    context.beginPath();
    context.ellipse(256, 329, 19, 8, 0, 0, Math.PI * 2);
    context.fill();
  } else if (mouth === 'thinking') {
    context.ellipse(270, 315, 13, 17, -0.25, 0, Math.PI * 2);
    context.fill();
  } else if (mouth === 'focused' || mouth === 'calm') {
    context.moveTo(235, 316);
    context.quadraticCurveTo(256, mouth === 'calm' ? 323 : 316, 278, 316);
    context.stroke();
  } else if (mouth === 'worried') {
    context.arc(256, 338, 29, Math.PI * 1.18, Math.PI * 1.82);
    context.stroke();
  } else {
    context.arc(256, 293, mouth === 'smile' ? 48 : 35, 0.18, Math.PI - 0.18);
    context.stroke();
  }
  agent.faceTexture.needsUpdate = true;
}

function drawLabel(agent: Agent): void {
  const context = agent.labelCanvas.getContext('2d');
  if (!context) return;
  const { width, height } = agent.labelCanvas;
  context.clearRect(0, 0, width, height);
  roundedRect(context, 5, 5, width - 10, height - 10, 48);
  context.fillStyle = 'rgba(10,20,43,.90)';
  context.fill();
  context.lineWidth = 7;
  context.strokeStyle = `#${agent.accent.getHexString()}`;
  context.stroke();
  context.fillStyle = '#ffffff';
  context.font = '600 48px system-ui, sans-serif';
  context.textAlign = 'center';
  context.textBaseline = 'middle';
  context.fillText(agent.role, width / 2, height / 2 + 2);
  agent.labelTexture.needsUpdate = true;
}

function createStaticFallback(container: HTMLElement): YuanbaoScene {
  const canvas = document.createElement('canvas');
  canvas.setAttribute('role', 'img');
  canvas.setAttribute('aria-label', 'Yuanbao green glass agents static scene');
  canvas.style.display = 'block';
  canvas.style.width = '100%';
  canvas.style.height = '100%';
  container.appendChild(canvas);
  const context = canvas.getContext('2d');
  let stage: Stage = 'idle';
  let count = MIN_AGENTS;
  let disposed = false;

  const draw = () => {
    if (disposed || !context) return;
    const width = Math.max(320, container.clientWidth || 640);
    const height = Math.max(240, container.clientHeight || 420);
    const ratio = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.round(width * ratio);
    canvas.height = Math.round(height * ratio);
    context.setTransform(ratio, 0, 0, ratio, 0, 0);
    context.clearRect(0, 0, width, height);
    const scale = Math.min(width / (count * 150), height / 400, 1);
    const spacing = 150 * scale;
    const center = width / 2;
    const y = height / 2 - 15;

    for (let index = 0; index < count; index += 1) {
      const x = center + (index - (count - 1) / 2) * spacing;
      const radius = 66 * scale;
      const glass = context.createRadialGradient(
        x - radius * 0.35,
        y - radius * 0.45,
        radius * 0.08,
        x,
        y,
        radius,
      );
      glass.addColorStop(0, '#a5ffe0');
      glass.addColorStop(0.28, '#24df91');
      glass.addColorStop(0.75, '#00b86b');
      glass.addColorStop(1, '#006f49');
      context.fillStyle = glass;
      context.beginPath();
      context.arc(x, y, radius, 0, Math.PI * 2);
      context.fill();
      context.strokeStyle = 'rgba(190,255,229,.65)';
      context.lineWidth = Math.max(1, 2 * scale);
      context.stroke();

      context.fillStyle = 'rgba(247,255,252,.96)';
      context.beginPath();
      context.ellipse(x, y + 7 * scale, 43 * scale, 33 * scale, 0, 0, Math.PI * 2);
      context.fill();
      context.save();
      context.translate(x, y + 3 * scale);
      context.scale(scale, scale);
      const leftKind: EyeKind = stage === 'complete' || stage === 'synthesizing'
        ? 'happy'
        : stage === 'cancelled' ? 'calm' : 'open';
      const rightKind: EyeKind = stage === 'complete'
        ? 'happy'
        : stage === 'analyzing' || stage === 'error' || stage === 'planning'
          ? 'open'
          : stage === 'cancelled' ? 'calm' : 'wink';
      drawEye(context, -17, -3, leftKind, stage === 'analyzing' ? 0.52 : 0.8);
      drawEye(context, 17, -3, rightKind, stage === 'analyzing' ? 0.52 : 0.8);
      context.strokeStyle = '#06251b';
      context.lineWidth = 5;
      context.lineCap = 'round';
      context.beginPath();
      if (stage === 'error') context.arc(0, 29, 10, Math.PI * 1.15, Math.PI * 1.85);
      else if (stage === 'planning') context.ellipse(4, 20, 5, 7, 0, 0, Math.PI * 2);
      else context.arc(0, 12, stage === 'complete' ? 17 : 12, 0.18, Math.PI - 0.18);
      context.stroke();
      context.restore();

      context.strokeStyle = 'rgba(255,255,255,.72)';
      context.lineWidth = Math.max(2, 5 * scale);
      context.lineCap = 'round';
      context.beginPath();
      context.arc(x, y, radius * 0.78, Math.PI * 1.12, Math.PI * 1.52);
      context.stroke();
      context.fillStyle = '#dce9ff';
      context.font = `${Math.max(11, 15 * scale)}px system-ui, sans-serif`;
      context.textAlign = 'center';
      context.fillText(roleAt(index, count), x, y + 92 * scale);
    }
    context.fillStyle = '#9fb4d8';
    context.font = '600 13px system-ui, sans-serif';
    context.textAlign = 'center';
    context.fillText(STAGE_LABELS[stage], center, height - 22);
  };

  const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(draw);
  observer?.observe(container);
  if (!observer) window.addEventListener('resize', draw);
  draw();

  return {
    setStage(value) {
      stage = normalizeStage(value);
      draw();
    },
    setAgentCount(value) {
      count = THREE.MathUtils.clamp(Math.round(value), MIN_AGENTS, MAX_AGENTS);
      draw();
    },
    dispose() {
      disposed = true;
      observer?.disconnect();
      if (!observer) window.removeEventListener('resize', draw);
      canvas.remove();
    },
  };
}

export function createYuanbaoScene(container: HTMLElement): YuanbaoScene {
  let renderer: THREE.WebGLRenderer;
  try {
    renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' });
  } catch {
    return createStaticFallback(container);
  }

  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.toneMapping = THREE.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.12;
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFSoftShadowMap;
  renderer.domElement.style.display = 'block';
  renderer.domElement.style.width = '100%';
  renderer.domElement.style.height = '100%';
  renderer.domElement.setAttribute('aria-label', 'Animated three-dimensional Yuanbao agents');
  container.appendChild(renderer.domElement);

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(38, 1, 0.1, 100);
  camera.position.set(0, 1.25, 11);
  camera.lookAt(0, -0.1, 0);

  const environmentCanvas = document.createElement('canvas');
  environmentCanvas.width = 1024;
  environmentCanvas.height = 512;
  const environmentContext = environmentCanvas.getContext('2d');
  if (environmentContext) {
    const background = environmentContext.createLinearGradient(0, 0, 0, 512);
    background.addColorStop(0, '#b8f5e1');
    background.addColorStop(0.42, '#173f38');
    background.addColorStop(1, '#031a17');
    environmentContext.fillStyle = background;
    environmentContext.fillRect(0, 0, 1024, 512);
    environmentContext.filter = 'blur(18px)';
    environmentContext.fillStyle = '#ffffff';
    environmentContext.fillRect(90, 70, 190, 72);
    environmentContext.fillRect(690, 100, 100, 240);
    environmentContext.fillStyle = '#75ffd0';
    environmentContext.fillRect(400, 350, 260, 42);
    environmentContext.filter = 'none';
  }
  const environmentTexture = new THREE.CanvasTexture(environmentCanvas);
  environmentTexture.mapping = THREE.EquirectangularReflectionMapping;
  environmentTexture.colorSpace = THREE.SRGBColorSpace;
  scene.environment = environmentTexture;
  scene.environmentIntensity = 1.1;

  scene.add(new THREE.HemisphereLight(0xe4fff4, 0x03251a, 2.1));
  const keyLight = new THREE.DirectionalLight(0xffffff, 3.8);
  keyLight.position.set(-4, 7, 7);
  keyLight.castShadow = true;
  keyLight.shadow.mapSize.set(1024, 1024);
  keyLight.shadow.camera.left = -7;
  keyLight.shadow.camera.right = 7;
  keyLight.shadow.camera.top = 7;
  keyLight.shadow.camera.bottom = -7;
  keyLight.shadow.bias = -0.0003;
  scene.add(keyLight);
  const rimLight = new THREE.PointLight(0x63ffd0, 30, 18, 2);
  rimLight.position.set(4, 2, 4);
  scene.add(rimLight);
  const fillLight = new THREE.PointLight(0xa5d8ff, 18, 16, 2);
  fillLight.position.set(-4, -1, 3);
  scene.add(fillLight);

  const ground = new THREE.Mesh(
    new THREE.PlaneGeometry(40, 40),
    new THREE.ShadowMaterial({ color: 0x07111f, opacity: 0.24 }),
  );
  ground.rotation.x = -Math.PI / 2;
  ground.position.y = -2.05;
  ground.receiveShadow = true;
  scene.add(ground);

  const sphereGeometry = new THREE.SphereGeometry(0.9, 48, 32);
  const bondGeometry = new THREE.CylinderGeometry(0.34, 0.34, 1, 16, 1, true);
  const agents: Agent[] = [];
  const bonds: THREE.Mesh<THREE.CylinderGeometry, THREE.MeshPhysicalMaterial>[] = [];
  let agentCount = 6;
  let stage: Stage = 'idle';
  let stageStarted = performance.now() / 1000;
  let previousTime = stageStarted;
  let frameId: number | null = null;
  let disposed = false;
  let pageVisible = !document.hidden;
  let intersecting = true;

  for (let index = 0; index < MAX_AGENTS; index += 1) {
    const group = new THREE.Group();
    const model = new THREE.Group();
    group.add(model);
    scene.add(group);

    const accent = accentAt(index, agentCount);
    const glassMaterial = new THREE.MeshPhysicalMaterial({
      color: new THREE.Color().setHSL(0.405 + index * 0.002, 0.88, 0.43),
      metalness: 0.02,
      roughness: 0.1,
      transmission: 0.68,
      thickness: 1.25,
      ior: 1.46,
      clearcoat: 1,
      clearcoatRoughness: 0.08,
      attenuationColor: new THREE.Color(0x00c978),
      attenuationDistance: 2.4,
      specularIntensity: 1,
    });
    const sphere = new THREE.Mesh(sphereGeometry, glassMaterial);
    sphere.castShadow = true;
    sphere.receiveShadow = true;
    model.add(sphere);

    const rim = new THREE.Mesh(
      sphereGeometry,
      new THREE.MeshBasicMaterial({
        color: 0x68ffd0,
        side: THREE.BackSide,
        transparent: true,
        opacity: 0.2,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
        toneMapped: false,
      }),
    );
    rim.scale.setScalar(1.018);
    model.add(rim);

    const faceCanvas = document.createElement('canvas');
    faceCanvas.width = 512;
    faceCanvas.height = 512;
    const faceTexture = new THREE.CanvasTexture(faceCanvas);
    faceTexture.colorSpace = THREE.SRGBColorSpace;
    faceTexture.anisotropy = Math.min(8, renderer.capabilities.getMaxAnisotropy());
    const face = new THREE.Mesh(
      sphereGeometry,
      new THREE.MeshBasicMaterial({ map: faceTexture, transparent: true, depthWrite: false, toneMapped: false }),
    );
    face.rotation.y = -Math.PI / 2;
    face.scale.setScalar(1.006);
    face.renderOrder = 1;
    model.add(face);

    const labelCanvas = document.createElement('canvas');
    labelCanvas.width = 512;
    labelCanvas.height = 128;
    const labelTexture = new THREE.CanvasTexture(labelCanvas);
    labelTexture.colorSpace = THREE.SRGBColorSpace;
    const label = new THREE.Sprite(new THREE.SpriteMaterial({
      map: labelTexture,
      transparent: true,
      depthTest: false,
      toneMapped: false,
    }));
    label.position.set(0, 2.05, 0);
    label.scale.set(2.1, 0.53, 1);
    group.add(label);

    const agent: Agent = {
      group,
      model,
      role: roleAt(index, agentCount),
      accent,
      position: new THREE.Vector3(),
      velocity: new THREE.Vector3(),
      target: new THREE.Vector3(),
      home: new THREE.Vector3(),
      squash: 0,
      squashVelocity: 0,
      contact: 0,
      labelCanvas,
      labelTexture,
      faceCanvas,
      faceTexture,
      lastFaceUpdate: -Infinity,
    };
    agents.push(agent);
    drawFace(agent, stage, 0);
    drawLabel(agent);
    model.scale.setScalar(1.64);
  }

  for (let index = 0; index < (MAX_AGENTS * (MAX_AGENTS - 1)) / 2; index += 1) {
    const material = new THREE.MeshPhysicalMaterial({
      color: 0x32e99a,
      metalness: 0.08,
      roughness: 0.18,
      transmission: 0.32,
      thickness: 0.5,
      clearcoat: 0.8,
      transparent: true,
      opacity: 0,
      depthWrite: false,
    });
    const bond = new THREE.Mesh(bondGeometry, material);
    bond.visible = false;
    scene.add(bond);
    bonds.push(bond);
  }

  const updateHomes = () => {
    agents.forEach((agent, index) => {
      const visible = index < agentCount;
      agent.group.visible = visible;
      if (!visible) return;
      const role = roleAt(index, agentCount);
      if (agent.role !== role) {
        agent.role = role;
        agent.accent.copy(accentAt(index, agentCount));
        drawLabel(agent);
      }
      const x = (index - (agentCount - 1) / 2) * Math.min(3.1, 17 / agentCount);
      const y = -0.55 + Math.cos((index / Math.max(1, agentCount - 1)) * Math.PI) * 0.32;
      agent.home.set(x, y, 0);
    });
  };

  const updateTargets = (time: number) => {
    const elapsed = time - stageStarted;
    agents.forEach((agent, index) => {
      if (index >= agentCount) return;
      agent.target.copy(agent.home);
      const isSearcher = agent.role.startsWith('Searcher');
      const searcherIndex = index - 1;
      const searcherCount = agentCount - 3;

      if (stage === 'planning') {
        if (agent.role === 'Planner') agent.target.add(new THREE.Vector3(0, 0.55, 1.25));
        if (isSearcher) agent.target.z = 0.35 + searcherIndex * 0.08;
      } else if (stage === 'searching') {
        if (isSearcher) {
          const layer = searcherIndex;
          agent.target.set(
            (layer % 2 === 0 ? -0.14 : 0.14) * Math.min(layer, 2),
            -1.08 + layer * (searcherCount > 3 ? 0.78 : 0.96),
            0.65 + layer * 0.06,
          );
        } else if (agent.role === 'Planner') {
          agent.target.set(-3.25, 0.15, -0.25);
        } else if (agent.role === 'Analyst') {
          agent.target.set(3.15, 0.1, -0.3);
        } else {
          agent.target.set(3.15, -1.05, -0.4);
        }
      } else if (stage === 'analyzing') {
        if (isSearcher) {
          agent.target.set(
            (searcherIndex % 2 === 0 ? -0.13 : 0.13) * Math.min(searcherIndex, 2),
            -1.08 + searcherIndex * (searcherCount > 3 ? 0.78 : 0.96),
            0.5,
          );
        } else if (agent.role === 'Analyst') {
          const angle = elapsed * 0.9;
          agent.target.set(Math.cos(angle) * 2.25, 0.25 + Math.sin(angle * 1.4) * 0.35, Math.sin(angle) * 1.25);
        }
      } else if (stage === 'synthesizing') {
        if (agent.role === 'Synthesizer') agent.target.add(new THREE.Vector3(0, 0.35, 0.8));
        if (isSearcher) {
          const blend = THREE.MathUtils.smoothstep(elapsed, 0.15, 2.2);
          const stacked = new THREE.Vector3(0, -0.8 + searcherIndex * 0.5, 0.5);
          agent.target.lerpVectors(stacked, agent.home, blend);
        }
      }
    });
  };

  const resetAgentTransforms = () => {
    agents.forEach((agent) => {
      agent.squash = 0;
      agent.squashVelocity = 0;
      agent.contact = 0;
      agent.model.scale.setScalar(1.64);
      agent.model.rotation.set(0, 0, 0);
    });
    bonds.forEach((bond) => { bond.visible = false; });
  };

  const render = () => renderer.render(scene, camera);

  const resize = () => {
    if (disposed) return;
    const width = Math.max(1, container.clientWidth || 640);
    const height = Math.max(1, container.clientHeight || 420);
    renderer.setSize(width, height, false);
    camera.aspect = width / height;
    const distanceForWidth = 9.4 / (Math.tan(THREE.MathUtils.degToRad(camera.fov / 2)) * camera.aspect);
    camera.position.z = Math.max(10.5, distanceForWidth);
    camera.updateProjectionMatrix();
    render();
  };

  const motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
  let reducedMotion = motionQuery.matches;
  const canAnimate = () => !disposed && !reducedMotion && pageVisible && intersecting;

  const snapToTargets = () => {
    const time = performance.now() / 1000;
    updateTargets(time);
    agents.forEach((agent, index) => {
      if (index >= agentCount) return;
      agent.position.copy(agent.target);
      agent.velocity.set(0, 0, 0);
      agent.group.position.copy(agent.position);
      drawFace(agent, stage, time);
      agent.lastFaceUpdate = time;
    });
    resetAgentTransforms();
    render();
  };

  const updateBonds = () => {
    let bondIndex = 0;
    const up = new THREE.Vector3(0, 1, 0);
    const direction = new THREE.Vector3();
    for (let left = 1; left < agentCount - 2; left += 1) {
      for (let right = left + 1; right < agentCount - 2; right += 1) {
        const a = agents[left];
        const b = agents[right];
        direction.subVectors(b.position, a.position);
        const distance = direction.length();
        const strength = THREE.MathUtils.clamp((1.38 - distance) / 0.7, 0, 1);
        if (strength <= 0.025) continue;
        const bond = bonds[bondIndex++];
        bond.visible = true;
        bond.position.copy(a.position).add(b.position).multiplyScalar(0.5);
        bond.quaternion.setFromUnitVectors(up, direction.normalize());
        bond.scale.set(0.42 * strength, Math.max(0.1, distance * 0.72), 0.42 * strength);
        bond.material.opacity = strength * 0.58;
      }
    }
    for (let index = bondIndex; index < bonds.length; index += 1) bonds[index].visible = false;
  };

  const animate = (milliseconds: number) => {
    frameId = null;
    if (!canAnimate()) return;
    const time = milliseconds / 1000;
    const delta = Math.min(0.033, Math.max(0.001, time - previousTime));
    previousTime = time;
    updateTargets(time);

    agents.forEach((agent, index) => {
      if (index >= agentCount) return;
      const idleMotion = stage === 'idle' || stage === 'complete';
      const floating = idleMotion ? Math.sin(time * 1.15 + index * 1.7) * (stage === 'idle' ? 0.1 : 0.035) : 0;
      const target = agent.target.clone();
      target.y += floating;
      const stiffness = stage === 'searching' ? 58 : 34;
      const damping = stage === 'searching' ? 10.5 : 12;
      agent.velocity.addScaledVector(target.sub(agent.position), stiffness * delta);
      agent.velocity.multiplyScalar(Math.exp(-damping * delta));
      agent.position.addScaledVector(agent.velocity, delta);
      agent.group.position.copy(agent.position);
      agent.model.rotation.z = THREE.MathUtils.clamp(-agent.velocity.x * 0.045, -0.22, 0.22);
      agent.model.rotation.y = Math.sin(time * 0.55 + index) * (idleMotion ? 0.08 : 0.025);
      const breathing = idleMotion ? Math.sin(time * 1.15 + index * 1.7) * 0.018 : 0;
      agent.model.scale.setScalar(1.64 * (1 + breathing));
    });

    agents.forEach((agent) => { agent.contact = 0; });
    if (stage === 'searching' || stage === 'analyzing') {
      for (let left = 1; left < agentCount - 2; left += 1) {
        for (let right = left + 1; right < agentCount - 2; right += 1) {
          const a = agents[left];
          const b = agents[right];
          const distance = a.position.distanceTo(b.position);
          const contact = THREE.MathUtils.clamp((1.42 - distance) / 0.75, 0, 1);
          a.contact = Math.max(a.contact, contact);
          b.contact = Math.max(b.contact, contact);
        }
      }
      updateBonds();
    } else {
      bonds.forEach((bond) => { bond.visible = false; });
    }

    agents.forEach((agent, index) => {
      if (index >= agentCount) return;
      const speedSquash = Math.min(0.12, agent.velocity.length() * 0.012);
      const desired = agent.contact * 0.34 + speedSquash;
      agent.squashVelocity += (desired - agent.squash) * 70 * delta;
      agent.squashVelocity *= Math.exp(-13 * delta);
      agent.squash += agent.squashVelocity * delta;
      const x = 1.64 * (1 + agent.squash * 0.48);
      const y = 1.64 * (1 - agent.squash * 0.52);
      const z = 1.64 * (1.64 * 1.64) / (x * y);
      agent.model.scale.set(x, y, z);
      if (time - agent.lastFaceUpdate >= 1 / 30) {
        drawFace(agent, stage, time);
        agent.lastFaceUpdate = time;
      }
    });
    render();
    frameId = requestAnimationFrame(animate);
  };

  const start = () => {
    if (!canAnimate() || frameId !== null) return;
    previousTime = performance.now() / 1000;
    frameId = requestAnimationFrame(animate);
  };

  const stop = () => {
    if (frameId !== null) cancelAnimationFrame(frameId);
    frameId = null;
  };

  const onVisibilityChange = () => {
    pageVisible = !document.hidden;
    if (pageVisible) start();
    else stop();
  };
  const onMotionChange = (event: MediaQueryListEvent) => {
    reducedMotion = event.matches;
    if (reducedMotion) {
      stop();
      snapToTargets();
    } else {
      start();
    }
  };
  document.addEventListener('visibilitychange', onVisibilityChange);
  if (motionQuery.addEventListener) motionQuery.addEventListener('change', onMotionChange);
  else motionQuery.addListener(onMotionChange);

  const resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(resize);
  resizeObserver?.observe(container);
  if (!resizeObserver) window.addEventListener('resize', resize);
  const intersectionObserver = typeof IntersectionObserver === 'undefined'
    ? null
    : new IntersectionObserver((entries) => {
      intersecting = entries[0]?.isIntersecting ?? true;
      if (intersecting) start();
      else stop();
    }, { threshold: 0.01 });
  intersectionObserver?.observe(container);

  updateHomes();
  resize();
  if (reducedMotion) snapToTargets();
  else {
    agents.forEach((agent) => {
      agent.position.copy(agent.home);
      agent.group.position.copy(agent.home);
    });
    render();
    start();
  }

  return {
    setStage(value) {
      if (disposed) return;
      stage = normalizeStage(value);
      stageStarted = performance.now() / 1000;
      if (stage !== 'searching' && stage !== 'analyzing') resetAgentTransforms();
      if (reducedMotion) snapToTargets();
      else start();
    },
    setAgentCount(value) {
      if (disposed) return;
      const next = THREE.MathUtils.clamp(Math.round(value), MIN_AGENTS, MAX_AGENTS);
      if (!Number.isFinite(next) || next === agentCount) return;
      agentCount = next;
      updateHomes();
      agents.forEach((agent, index) => {
        if (index === agentCount - 1 || index === agentCount - 2) drawLabel(agent);
        if (index >= agentCount) return;
        if (agent.position.lengthSq() === 0) agent.position.copy(agent.home);
      });
      if (reducedMotion) snapToTargets();
      else render();
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      stop();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      if (motionQuery.removeEventListener) motionQuery.removeEventListener('change', onMotionChange);
      else motionQuery.removeListener(onMotionChange);
      resizeObserver?.disconnect();
      if (!resizeObserver) window.removeEventListener('resize', resize);
      intersectionObserver?.disconnect();
      const geometries = new Set<THREE.BufferGeometry>();
      const materials = new Set<THREE.Material>();
      const textures = new Set<THREE.Texture>([environmentTexture]);
      scene.environment = null;
      scene.traverse((object) => {
        const renderable = object as THREE.Mesh;
        if (renderable.geometry) geometries.add(renderable.geometry);
        const material = renderable.material;
        if (Array.isArray(material)) material.forEach((item) => materials.add(item));
        else if (material) materials.add(material);
      });
      agents.forEach((agent) => {
        textures.add(agent.faceTexture);
        textures.add(agent.labelTexture);
      });
      textures.forEach((texture) => texture.dispose());
      materials.forEach((material) => material.dispose());
      geometries.forEach((geometry) => geometry.dispose());
      renderer.dispose();
      renderer.forceContextLoss();
      renderer.domElement.remove();
    },
  };
}
