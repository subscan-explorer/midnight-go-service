FROM node:22
WORKDIR /bench
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
