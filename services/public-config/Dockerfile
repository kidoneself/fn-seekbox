FROM scratch
COPY build/server /app/server
COPY frontend/dist /app/web
COPY seed.json /app/seed.json
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/server"]
