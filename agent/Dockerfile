FROM node:22.19.0-alpine AS build
WORKDIR /agent
COPY agent/package*.json ./
RUN npm ci --ignore-scripts
COPY agent/tsconfig.json ./
COPY agent/src ./src
RUN npm run build

FROM node:22.19.0-alpine
WORKDIR /agent
ENV NODE_ENV=production
COPY agent/package*.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force
COPY --from=build /agent/dist/src ./dist/src
USER node
CMD ["node", "dist/src/server.js"]
